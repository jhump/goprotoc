package goprotoc

import (
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// errOverflow is returned when an integer is too large to be represented.
var errOverflow = errors.New("proto: integer overflow")

type codedReader struct {
	buf   []byte
	index int
}

func newCodedReader(buf []byte) *codedReader {
	return &codedReader{buf: buf}
}

func (cb *codedReader) eof() bool {
	return cb.index >= len(cb.buf)
}

func (cb *codedReader) skip(count int) bool {
	newIndex := cb.index + count
	if newIndex > len(cb.buf) {
		return false
	}
	cb.index = newIndex
	return true
}

func (cb *codedReader) decodeVarintSlow() (x uint64, err error) {
	i := cb.index
	l := len(cb.buf)

	for shift := uint(0); shift < 64; shift += 7 {
		if i >= l {
			err = io.ErrUnexpectedEOF
			return
		}
		b := cb.buf[i]
		i++
		x |= (uint64(b) & 0x7F) << shift
		if b < 0x80 {
			cb.index = i
			return
		}
	}

	// The number is too large to represent in a 64-bit value.
	err = errOverflow
	return
}

// DecodeVarint reads a varint-encoded integer from the Buffer.
// This is the format for the
// int32, int64, uint32, uint64, bool, and enum
// protocol buffer types.
func (cb *codedReader) decodeVarint() (uint64, error) {
	i := cb.index
	buf := cb.buf

	if i >= len(buf) {
		return 0, io.ErrUnexpectedEOF
	} else if buf[i] < 0x80 {
		cb.index++
		return uint64(buf[i]), nil
	} else if len(buf)-i < 10 {
		return cb.decodeVarintSlow()
	}

	var b uint64
	// we already checked the first byte
	x := uint64(buf[i]) - 0x80
	i++

	b = uint64(buf[i])
	i++
	x += b << 7
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 7

	b = uint64(buf[i])
	i++
	x += b << 14
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 14

	b = uint64(buf[i])
	i++
	x += b << 21
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 21

	b = uint64(buf[i])
	i++
	x += b << 28
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 28

	b = uint64(buf[i])
	i++
	x += b << 35
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 35

	b = uint64(buf[i])
	i++
	x += b << 42
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 42

	b = uint64(buf[i])
	i++
	x += b << 49
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 49

	b = uint64(buf[i])
	i++
	x += b << 56
	if b&0x80 == 0 {
		goto done
	}
	x -= 0x80 << 56

	b = uint64(buf[i])
	i++
	x += b << 63
	if b&0x80 == 0 {
		goto done
	}
	// x -= 0x80 << 63 // Always zero.

	return 0, errOverflow

done:
	cb.index = i
	return x, nil
}

func (cb *codedReader) decodeTagAndWireType() (tag int32, wireType protowire.Type, err error) {
	var v uint64
	v, err = cb.decodeVarint()
	if err != nil {
		return
	}
	// low 7 bits is wire type
	wireType = protowire.Type(v & 7)
	// rest is int32 tag number
	v = v >> 3
	if v > math.MaxInt32 {
		err = fmt.Errorf("tag number out of range: %d", v)
		return
	}
	tag = int32(v)
	return
}

// DecodeFixed64 reads a 64-bit integer from the Buffer.
// This is the format for the
// fixed64, sfixed64, and double protocol buffer types.
func (cb *codedReader) decodeFixed64() (x uint64, err error) {
	// x, err already 0
	i := cb.index + 8
	if i < 0 || i > len(cb.buf) {
		err = io.ErrUnexpectedEOF
		return
	}
	cb.index = i

	x = uint64(cb.buf[i-8])
	x |= uint64(cb.buf[i-7]) << 8
	x |= uint64(cb.buf[i-6]) << 16
	x |= uint64(cb.buf[i-5]) << 24
	x |= uint64(cb.buf[i-4]) << 32
	x |= uint64(cb.buf[i-3]) << 40
	x |= uint64(cb.buf[i-2]) << 48
	x |= uint64(cb.buf[i-1]) << 56
	return
}

// DecodeFixed32 reads a 32-bit integer from the Buffer.
// This is the format for the
// fixed32, sfixed32, and float protocol buffer types.
func (cb *codedReader) decodeFixed32() (x uint64, err error) {
	// x, err already 0
	i := cb.index + 4
	if i < 0 || i > len(cb.buf) {
		err = io.ErrUnexpectedEOF
		return
	}
	cb.index = i

	x = uint64(cb.buf[i-4])
	x |= uint64(cb.buf[i-3]) << 8
	x |= uint64(cb.buf[i-2]) << 16
	x |= uint64(cb.buf[i-1]) << 24
	return
}

// DecodeRawBytes reads a count-delimited byte buffer from the Buffer.
// This is the format used for the bytes protocol buffer
// type and for embedded messages.
func (cb *codedReader) decodeRawBytes(alloc bool) (buf []byte, err error) {
	n, err := cb.decodeVarint()
	if err != nil {
		return nil, err
	}

	nb := int(n)
	if nb < 0 {
		return nil, fmt.Errorf("proto: bad byte length %d", nb)
	}
	end := cb.index + nb
	if end < cb.index || end > len(cb.buf) {
		return nil, io.ErrUnexpectedEOF
	}

	if !alloc {
		buf = cb.buf[cb.index:end]
		cb.index += nb
		return
	}

	buf = make([]byte, nb)
	copy(buf, cb.buf[cb.index:])
	cb.index += nb
	return
}

const (
	// MaxTag is the maximum allowed tag number for a field.
	maxTag = 536870911 // 2^29 - 1

	// SpecialReservedStart is the first tag in a range that is reserved and not
	// allowed for use in message definitions.
	specialReservedStart = 19000
	// SpecialReservedEnd is the last tag in a range that is reserved and not
	// allowed for use in message definitions.
	specialReservedEnd = 19999
)

func isProbablyMessage(data []byte) bool {
	in := newCodedReader(data)
	return in.isProbablyMessage(false)
}

func (cb *codedReader) isProbablyMessage(inGroup bool) bool {
	for {
		if cb.eof() {
			// if in group, we should find "end group" tag before EOF
			return !inGroup
		}
		t, w, err := cb.decodeTagAndWireType()
		if err != nil {
			return false
		}
		if w == protowire.EndGroupType {
			return inGroup
		}
		if t < 1 || t > maxTag {
			return false
		}
		if t >= specialReservedStart && t <= specialReservedEnd {
			return false
		}
		switch w {
		case protowire.VarintType:
			// skip varint by finding last byte (has high bit unset)
			i := cb.index
			limit := i + 10 // varint cannot be >10 bytes
			bs := cb.buf
			for {
				if i >= len(bs) || i >= limit {
					return false
				}
				if bs[i]&0x80 == 0 {
					break
				}
				i++
			}
			cb.index = i + 1
		case protowire.Fixed32Type:
			if !cb.skip(4) {
				return false
			}
		case protowire.Fixed64Type:
			if !cb.skip(8) {
				return false
			}
		case protowire.BytesType:
			l, err := cb.decodeVarint()
			if err != nil {
				return false
			}
			if !cb.skip(int(l)) {
				return false
			}
		case protowire.StartGroupType:
			if !cb.isProbablyMessage(true) {
				return false
			}
		default:
			// invalid wire type
			return false
		}
	}
}

func isProbablyString(data []byte) bool {
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		if r == utf8.RuneError {
			return false
		}
		data = data[n:]
	}
	return true
}
