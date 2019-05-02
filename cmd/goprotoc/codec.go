package main

import (
	"io"

	"github.com/jhump/protoreflect/desc"
)

func doEncode(encodeType string, fds []*desc.FileDescriptor, r io.Reader) error {
	// TODO
	return nil
}

func doDecode(decodeType string, fds []*desc.FileDescriptor, r io.Reader) error {
	// TODO
	return nil
}

func doDecodeRaw(fds []*desc.FileDescriptor, r io.Reader) error {
	// TODO
	return nil
}