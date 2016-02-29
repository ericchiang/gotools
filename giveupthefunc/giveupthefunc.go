package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/types"
)

var help = `usage: giveupthefunc [-i] [-a] <list of packages>

giveupthefunc counts the number of times function calls are used.

Flags:

	-i	Don't count function calls of functions that are used to satisfy interfaces.

	-a	Allow errors when loading packages. Packages with errors will be omitted from results. 
`

func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(2)
}

func main() {
	interfaceAnalysis := false
	allowErrors := false
	flag.BoolVar(&interfaceAnalysis, "i", false, "")
	flag.BoolVar(&allowErrors, "a", false, "")
	flag.Parse()

	args := append([]string{"list"}, flag.Args()...)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderr.WriteTo(os.Stderr)
		os.Exit(2)
	}
	pkgs := strings.Split(string(bytes.TrimSpace(stdout.Bytes())), "\n")

	config := loader.Config{AllowErrors: allowErrors}
	if allowErrors {
		config.TypeChecker.Error = func(error) {}
	}
	for _, pkg := range pkgs {
		config.Import(pkg)
	}

	program, err := config.Load()
	if err != nil {
		fatal(err)
	}

	var interfaces map[types.Object]*types.Interface
	if interfaceAnalysis {
		interfaces = allInterfaces(program)
	}

	defs := make(map[types.Object]int)
	for _, pkg := range pkgs {
		info := program.Imported[pkg]
		if allowErrors && len(info.Errors) != 0 {
			continue
		}
		for _, obj := range info.Defs {
			if obj == nil {
				continue
			}
			if f, ok := obj.(*types.Func); ok {
				switch obj.Name() {
				case "main", "init":
					continue
				}
				if interfaceAnalysis && satisfiesInterface(f, interfaces) {
					continue
				}
				defs[obj] = 0
			}
		}
	}

	// Count number of times each definition is used.
	for _, pkg := range pkgs {
		info := program.Imported[pkg]
		if allowErrors && len(info.Errors) != 0 {
			continue
		}
		for _, obj := range info.Uses {
			if obj == nil {
				continue
			}
			if _, ok := defs[obj]; ok {
				defs[obj]++
			}
		}
	}
	i := 0
	counts := make([]defCount, len(defs))
	for obj, count := range defs {
		counts[i] = defCount{obj, count}
		i++
	}
	sort.Sort(byCount(counts))
	for _, count := range counts {
		fmt.Printf("\t%d\t%s\n", count.count, objString(count.obj))
	}
}

func objString(obj types.Object) string {
	f, ok := obj.(*types.Func)
	if !ok {
		return obj.String()
	}
	return f.FullName()
}

type defCount struct {
	obj   types.Object
	count int
}

type byCount []defCount

func (b byCount) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byCount) Len() int      { return len(b) }

func (b byCount) Less(i, j int) bool {
	if b[i].count != b[j].count {
		return b[i].count < b[j].count
	}
	return b[i].obj.String() < b[j].obj.String()
}

func allInterfaces(prog *loader.Program) map[types.Object]*types.Interface {
	interfaces := map[types.Object]*types.Interface{}
	for _, info := range prog.AllPackages {
		if len(info.Errors) != 0 {
			continue
		}
		for _, obj := range info.Defs {
			if obj == nil {
				continue
			}
			if inter, ok := obj.Type().Underlying().(*types.Interface); ok {
				if inter.NumMethods() == 0 {
					// interface{} says nothing.
					continue
				}
				interfaces[obj] = inter
			}
		}
	}
	return interfaces
}

func satisfiesInterface(f *types.Func, interfaces map[types.Object]*types.Interface) bool {
	sig, ok := f.Type().(*types.Signature)
	if !ok {
		return false
	}
	v := sig.Recv()
	if v == nil {
		return false
	}
	for obj, inter := range interfaces {
		samePkg := obj.Pkg() != nil && obj.Pkg() == f.Pkg()
		if (!samePkg) && (!obj.Exported() || !f.Exported()) {
			continue
		}
		for i := 0; i < inter.NumMethods(); i++ {
			m := inter.Method(i)
			if types.Identical(sig, m.Type()) {
				return true
			}
		}
	}
	return false
}
