package MumbleProto_test

import (
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/golang/protobuf/proto"
)

// The messages keep their bytes on the wire: encoding gives the recorded
// bytes, decoding them gives the same fields, and a field this schema does
// not know survives decoding and encoding again.
func TestWireFormat(t *testing.T) {
	for _, c := range wireCases {
		msg := c.msg.(proto.Message)
		name := reflect.TypeOf(msg).Elem().Name()
		fill(msg)
		got, err := proto.Marshal(msg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if hex.EncodeToString(got) != c.wire {
			t.Errorf("%s: encoded as\n%x\nwant\n%s", name, got, c.wire)
			continue
		}

		decoded := reflect.New(reflect.TypeOf(msg).Elem()).Interface().(proto.Message)
		if err := proto.Unmarshal(got, decoded); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !sameFields(msg, decoded) {
			t.Errorf("%s: decoded fields differ", name)
		}

		withUnknown := append(got, unknownField...)
		decoded = reflect.New(reflect.TypeOf(msg).Elem()).Interface().(proto.Message)
		if err := proto.Unmarshal(withUnknown, decoded); err != nil {
			t.Fatalf("%s with an unknown field: %v", name, err)
		}
		if !sameFields(msg, decoded) {
			t.Errorf("%s with an unknown field: decoded fields differ", name)
		}
		again, err := proto.Marshal(decoded)
		if err != nil {
			t.Fatalf("%s with an unknown field: %v", name, err)
		}
		if string(again) != string(withUnknown) {
			t.Errorf("%s: unknown field not kept:\n%x\nwant\n%x", name, again, withUnknown)
		}
	}
}
