package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/mattn/go-isatty"
	"golang.org/x/tools/go/loader"
)

var help = `usage: gosearch [flags] <expression> [packages]

gosearch performs a type aware search on a list of provided packages.

The expression is a package followed by a top level type.

	gosearch 'net.Listen' net/http/...

Or a field on a type.

	gosearch 'bytes.Buffer.String' text/template

Package names must be quoted if they contain a period.

	gosearch '"golang.org/x/tools/go/loader".Config.Import' .

The command accepts the following flags:

	-t	Load and search *_test.go files for use of the expression. 

	-a	Allow build errors. Packages that fail to build with be omitted from the search. 

	-d	Search for declarations of expressions instead of uses.
`

// fatal prints the provided arguments to stderr and exits.
func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(2)
}

var showColors = isatty.IsTerminal(os.Stdout.Fd())

func main() {
	conf := config{}

	flag.Usage = func() {
		fatal(help)
	}
	flag.BoolVar(&conf.importTests, "t", false, "")
	flag.BoolVar(&conf.allowErrors, "a", false, "")
	flag.BoolVar(&conf.searchDefs, "d", false, "")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 || args[0] == "" {
		fatal(help)
	}
	targetPkg, name, fields, err := splitTarget(args[0])
	if err != nil {
		fatal(err, help)
	}
	pkgs, err := golist(flag.Args()[1:]...)
	if err != nil {
		fatal(err)
	}

	conf.targetPkg = targetPkg
	conf.fieldName = name
	conf.subFields = fields
	conf.packages = pkgs

	fset, idents, err := conf.search()
	if err != nil {
		fatal(err)
	}

	sort.Sort(byPos(idents))
	for _, ident := range idents {
		if err := printLine(fset, ident); err != nil {
			fatal(err)
		}
	}
}

type config struct {
	targetPkg   string
	fieldName   string
	subFields   []string
	packages    []string
	allowErrors bool
	importTests bool
	searchDefs  bool
}

func (c *config) search() (*token.FileSet, []*ast.Ident, error) {
	// Load and evaluate the types of the target package and all packages
	// which import it.
	config := loader.Config{AllowErrors: c.allowErrors}
	if c.allowErrors {
		config.TypeChecker.Error = func(error) {}
	}
	importPkg := config.Import
	if c.importTests {
		importPkg = config.ImportWithTests
	}
	importPkg(c.targetPkg)
	for _, pkg := range c.packages {
		importPkg(pkg)
	}
	prog, err := config.Load()
	if err != nil {
		return nil, nil, err
	}

	// Determine the type of the provided expression.
	obj, err := lookupObject(prog.Imported[c.targetPkg], c.fieldName, c.subFields...)
	if err != nil {
		return nil, nil, err
	}

	// Search for uses of that type.
	var idents []*ast.Ident
	for _, pkg := range c.packages {
		info := prog.Imported[pkg]
		if len(info.Errors) != 0 {
			continue
		}
		identsMap := info.Uses
		if c.searchDefs {
			identsMap = info.Defs
		}
		for ident, o := range identsMap {
			if o == obj {
				idents = append(idents, ident)
			}
		}
	}
	return prog.Fset, idents, nil
}

type byPos []*ast.Ident

func (p byPos) Len() int           { return len(p) }
func (p byPos) Less(i, j int) bool { return p[i].NamePos < p[j].NamePos }
func (p byPos) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// golist passes the provided arguments into the 'go list' command
// returning a list of packages.
func golist(args ...string) ([]string, error) {
	if _, err := exec.LookPath("go"); err != nil {
		return nil, errors.New("could not find the go tool in PATH")
	}
	args = append([]string{"list"}, args...)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New(stderr.String())
	}
	return strings.Split(string(bytes.TrimSpace(stdout.Bytes())), "\n"), nil
}

// lookupObject attempts to find the type of the specified field name.
func lookupObject(pkgInfo *loader.PackageInfo, name string, fields ...string) (types.Object, error) {
	if len(pkgInfo.Errors) != 0 {
		return nil, fmt.Errorf("Package '%s' had compilation errors", pkgInfo.Pkg.Path())
	}
	pkg := pkgInfo.Pkg
	obj := pkg.Scope().Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("Failed to find type '%s' in package '%s'", name, pkg.Path())
	}
	for i, field := range fields {
		obj, _, _ = types.LookupFieldOrMethod(obj.Type(), true, pkg, field)
		if obj == nil {
			return nil, fmt.Errorf("Failed to lookup field or method '%s' on type '%s'", strings.Join(fields[:i+1], "."), name)
		}
	}
	return obj, nil
}

type fileErr struct {
	pos token.Position
	err error
}

func (f *fileErr) Error() string {
	return fmt.Sprintf("%s:%d:%v", f.pos.Filename, f.pos.Line, f.err)
}

func printLine(fset *token.FileSet, ident *ast.Ident) error {
	pos := fset.Position(ident.NamePos)

	lineStart := int64(pos.Offset - (pos.Column - 1))

	f, err := os.OpenFile(pos.Filename, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Seek(lineStart, 0); err != nil {
		return &fileErr{pos, err}
	}

	r := bufio.NewReader(f)
	line, err := r.ReadString('\n')
	if err != nil {
		if err != io.EOF {
			return &fileErr{pos, err}
		}
		line += "\n"
	}
	start := pos.Column - 1
	end := fset.Position(ident.End()).Column - 1
	if len(line) < end {
		return &fileErr{pos, errors.New("identifier extends past end of line")}
	}
	if showColors {
		line = line[:start] + color(line[start:end]) + line[end:]
	}
	filename := pos.Filename
	if cwd, err := os.Getwd(); err == nil {
		if strings.HasPrefix(filename, cwd) {
			filename = "." + filename[len(cwd):]
		}
	}
	fmt.Printf("%s:%d:%s", filename, pos.Line, line)

	return nil
}

// splitTarget performs a quote aware split by periods. Periods within
// double quotes are ignored, and quotes are not part of the returned
// strings.
//
//     fmt.Println(splitTarget(`"github.com/ericchiang/gosearch".Foo.Bar`))
//     // github.com/ericchiang/gosearch Foo [Bar] nil
//
func splitTarget(s string) (pkg, name string, fields []string, err error) {
	pkg, s, err = readNext(s)
	if err != nil {
		return
	}
	if pkg == "" {
		return "", "", nil, errors.New("no target provided")
	}
	name, s, err = readNext(s)
	if err != nil {
		return
	}
	if name == "" {
		return "", "", nil, errors.New("no package field provided")
	}
	for {
		var field string
		field, s, err = readNext(s)
		if err != nil {
			return
		}
		if field == "" {
			return
		}
		fields = append(fields, field)
	}
}

func readNext(s string) (string, string, error) {
	var field, rest bytes.Buffer
	r := strings.NewReader(s)
	inQuote := false
Loop:
	for {
		r, _, err := r.ReadRune()
		if err != nil {
			if inQuote {
				return "", "", errors.New(`unmatched '"'`)
			}
			break // only error we can get is io.EOF
		}
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == '.' && !inQuote:
			break Loop
		default:
			field.WriteRune(r)
		}
	}
	io.Copy(&rest, r)
	return field.String(), rest.String(), nil
}
