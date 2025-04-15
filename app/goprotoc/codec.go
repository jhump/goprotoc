package goprotoc

import (
	"bytes"
	"fmt"
	"io"
	"math"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"google.golang.org/protobuf/encoding/protowire"
)

func doEncode(encodeType string, fds []*desc.FileDescriptor, r io.Reader, w io.Writer) error {
	var md *desc.MessageDescriptor
	for _, fd := range fds {
		md = fd.FindMessage(encodeType)
		if md != nil {
			break
		}
	}
	if md == nil {
		return fmt.Errorf("type not defined: %s", encodeType)
	}

	var er dynamic.ExtensionRegistry
	for _, fd := range fds {
		er.AddExtensionsFromFileRecursively(fd)
	}
	dm := dynamic.NewMessageWithExtensionRegistry(md, &er)

	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read input: %v", err)
	}
	if err := dm.UnmarshalText(b); err != nil {
		return fmt.Errorf("failed to parse input: %v", err)
	}
	b, err = dm.Marshal()
	if err != nil {
		return fmt.Errorf("failed to serialize message: %v", err)
	}
	_, err = w.Write(b)
	if err != nil {
		return fmt.Errorf("failed to write encoded message: %v", err)
	}
	return nil
}

func doDecode(decodeType string, fds []*desc.FileDescriptor, r io.Reader, w io.Writer) error {
	var md *desc.MessageDescriptor
	for _, fd := range fds {
		md = fd.FindMessage(decodeType)
		if md != nil {
			break
		}
	}
	if md == nil {
		return fmt.Errorf("type not defined: %s", decodeType)
	}

	var er dynamic.ExtensionRegistry
	for _, fd := range fds {
		er.AddExtensionsFromFileRecursively(fd)
	}
	dm := dynamic.NewMessageWithExtensionRegistry(md, &er)

	b, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read input: %v", err)
	}
	if err := dm.Unmarshal(b); err != nil {
		return fmt.Errorf("failed to parse input: %v", err)
	}
	b, err = dm.MarshalTextIndent()
	if err != nil {
		return fmt.Errorf("failed to format message: %v", err)
	}
	_, err = w.Write(b)
	if err != nil {
		return fmt.Errorf("failed to write decoded message: %v", err)
	}
	return nil
}

func doDecodeRaw(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	in := newCodedReader(data)
	return decodeRawMessage(in, w, "", false)
}

func decodeRawMessage(in *codedReader, w io.Writer, indent string, inGroup bool) error {
	for {
		if in.eof() {
			if inGroup {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		t, wt, err := in.decodeTagAndWireType()
		if err != nil {
			return err
		}
		if wt == protowire.EndGroupType {
			if inGroup {
				return nil
			}
			return fmt.Errorf("input contains unexpected 'end group' wire type")
		}
		if t < 1 || t > maxTag {
			return fmt.Errorf("input contains illegal tag number: %d", t)
		}
		if t >= specialReservedStart && t <= specialReservedEnd {
			return fmt.Errorf("input contains illegal tag number: %d", t)
		}
		switch wt {
		case protowire.VarintType:
			v, err := in.decodeVarint()
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s%d: %d\n", indent, t, v); err != nil {
				return err
			}
		case protowire.Fixed32Type:
			v, err := in.decodeFixed32()
			if err != nil {
				return err
			}
			f := math.Float32frombits(uint32(v))
			if _, err := fmt.Fprintf(w, "%s%d: %f\n", indent, t, f); err != nil {
				return err
			}
		case protowire.Fixed64Type:
			v, err := in.decodeFixed64()
			if err != nil {
				return err
			}
			f := math.Float64frombits(v)
			if _, err := fmt.Fprintf(w, "%s%d: %f\n", indent, t, f); err != nil {
				return err
			}
		case protowire.BytesType:
			v, err := in.decodeRawBytes(false)
			if err != nil {
				return err
			}
			if isProbablyMessage(v) {
				if _, err := fmt.Fprintf(w, "%s%d: <\n", indent, t); err != nil {
					return err
				}
				nested := newCodedReader(v)
				if err := decodeRawMessage(nested, w, indent+"  ", false); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "%s>\n", indent); err != nil {
					return err
				}
			} else if isProbablyString(v) {
				if _, err := fmt.Fprintf(w, "%s%d: %s\n", indent, t, quoteString(v)); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintf(w, "%s%d: %s\n", indent, t, quoteBytes(v)); err != nil {
					return err
				}
			}
		case protowire.StartGroupType:
			if _, err := fmt.Fprintf(w, "%s%d {\n", indent, t); err != nil {
				return err
			}
			if err := decodeRawMessage(in, w, indent+"  ", true); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "%s}\n", indent); err != nil {
				return err
			}
		default:
			// invalid wire type
			return fmt.Errorf("input contained invalid wire type: %d", wt)
		}
	}
}

func quoteString(s []byte) string {
	// strings.Builder returns nil error for all Write* methods,
	// so we ignore error return values in method calls below
	var buf bytes.Buffer
	// use WriteByte here to get any needed indent
	_ = buf.WriteByte('"')
	// Loop over the bytes, not the runes.
	for i := 0; i < len(s); i++ {
		// Divergence from C++: we don't escape apostrophes.
		// There's no need to escape them, and the C++ parser
		// copes with a naked apostrophe.
		switch c := s[i]; c {
		case '\n':
			_, _ = buf.WriteString("\\n")
		case '\r':
			_, _ = buf.WriteString("\\r")
		case '\t':
			_, _ = buf.WriteString("\\t")
		case '"':
			_, _ = buf.WriteString("\\")
		case '\\':
			_, _ = buf.WriteString("\\\\")
		default:
			if c >= 0x20 && c < 0x7f {
				_ = buf.WriteByte(c)
			} else {
				_, _ = fmt.Fprintf(&buf, "\\%03o", c)
			}
		}
	}
	_ = buf.WriteByte('"')
	return buf.String()
}

func quoteBytes(b []byte) string {
	// strings.Builder returns nil error for all Write* methods,
	// so we ignore error return values in method calls below
	var buf bytes.Buffer
	// use WriteByte here to get any needed indent
	_ = buf.WriteByte('"')
	// Loop over the bytes, not the runes.
	for i := 0; i < len(b); i++ {
		// for bytes, we just hex-encode everything
		_, _ = fmt.Fprintf(&buf, "\\%03o", b[i])
	}
	_ = buf.WriteByte('"')
	return buf.String()
}
