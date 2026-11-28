package search

import (
	"reflect"
	"strconv"
	"testing"
)

func TestProtoFieldsPaths(t *testing.T) {
	t.Parallel()
	usage := append(append(pbVarint(2, 23949), pbVarint(3, 1497)...), pbVarint(5, 20398)...)
	msg := append(append(pb(19, []byte("gemini-3.8-flash")), pb(7, usage)...), pbVarint(1, 42)...)

	got := map[string]any{}
	for _, f := range ProtoFields(msg) {
		key := ""
		for _, p := range f.Path {
			key += "/" + strconv.Itoa(p)
		}
		if f.Wire == 0 {
			got[key] = f.Varint
		} else if f.Path[len(f.Path)-1] == 19 {
			got[key] = string(f.Bytes)
		}
	}
	want := map[string]any{
		"/19": "gemini-3.8-flash", "/7/2": uint64(23949), "/7/3": uint64(1497), "/7/5": uint64(20398), "/1": uint64(42),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestProtoFieldsRejectsGarbage(t *testing.T) {
	t.Parallel()
	if f := ProtoFields([]byte{0x0a, 0xff}); f != nil {
		t.Fatalf("truncated message decoded: %v", f)
	}
	deep := pbVarint(1, 1)
	for i := 0; i < maxDepth+5; i++ {
		deep = pb(1, deep)
	}
	for _, f := range ProtoFields(deep) {
		if len(f.Path) > maxDepth+1 {
			t.Fatalf("followed nesting past maxDepth: path length %d", len(f.Path))
		}
	}
}
