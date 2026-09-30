package docsync

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"
)

// BenchmarkCheck exercises the §33 targets on a synthetic tree: many code
// files with one def each and docs citing them. It is a benchmark, not a
// gate; run `go test -bench Check -benchmem .` to read the numbers.
func BenchmarkCheck(b *testing.B) {
	for _, n := range []int{100, 1000} {
		fsys := fstest.MapFS{}
		for i := 0; i < n; i++ {
			fsys[fmt.Sprintf("internal/p%d/f.go", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("package p\n\n// ds:def id=fn%d-a2b6f8jk\nfunc F%d() int {\n\treturn %d\n}\n", i, i, i))}
			if i%10 == 0 {
				fsys[fmt.Sprintf("docs/d%d.md", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("See [f](ds:block?id=fn%d-a2b6f8jk).\n", i))}
			}
		}
		s, err := New(WithFS(fsys))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := s.Check(context.Background(), CheckOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
