package main

import (
	"fmt"
	"reflect"
	"testing"
)

func TestSplitTarget(t *testing.T) {
	tests := []struct {
		s       string
		pkg     string
		name    string
		fields  []string
		wantErr bool
	}{
		{
			s:    `hello.world`,
			pkg:  "hello",
			name: "world",
		},
		{
			s:      `"github.com/ericchiang/gosearch".Foo.Bar`,
			pkg:    "github.com/ericchiang/gosearch",
			name:   "Foo",
			fields: []string{"Bar"},
		},
	}

	for _, tt := range tests {
		errorf := func(format string, a ...interface{}) {
			prefix := fmt.Sprintf("splitTarget(%q): ", tt.s)
			t.Errorf(prefix+format, a...)
		}

		pkg, name, fields, err := splitTarget(tt.s)
		if err != nil {
			if !tt.wantErr {
				errorf("%v", err)
			}
			continue
		}
		if tt.wantErr {
			errorf("expected error")
		}

		if pkg != tt.pkg {
			errorf("expected pkg=%q, got=%q", tt.pkg, pkg)
		}
		if name != tt.name {
			errorf("expected name=%q, got=%q", tt.name, name)
		}
		if !reflect.DeepEqual(tt.fields, fields) {
			errorf("expected fields=%q, got=%q", tt.fields, fields)
		}
	}
}

func BenchmarkSearch(b *testing.B) {
	stdLib, err := golist("std")
	if err != nil {
		b.Fatal(err)
	}
	config := config{
		targetPkg: "net",
		fieldName: "Dial",
		packages:  stdLib,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := config.search(); err != nil {
			b.Fatal(err)
		}
	}
}
