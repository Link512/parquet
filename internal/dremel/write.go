package dremel

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"text/template"

	"github.com/parsyl/parquet/internal/parse"
	"github.com/parsyl/parquet/internal/structs"
)

func init() {
	funcs := template.FuncMap{
		"removeStar": func(s string) string {
			return strings.Replace(s, "*", "", 1)
		},
	}

	var err error
	writeTpl, err = template.New("output").Funcs(funcs).Parse(`func write{{.FuncName}}(x *{{.Type}}, vals []{{removeStar .TypeName}}, def int64) bool {
	switch def { {{range .Cases}}
	{{.}}{{end}} }
	return false
}`)
	if err != nil {
		log.Fatal(err)
	}

	writeTpl, err = writeTpl.Parse(`{{define "initStructs"}}{{range .}}{{.}}{{end}}{{end}}`)
	if err != nil {
		log.Fatal(err)
	}
}

var (
	writeTpl *template.Template
)

type writeInput struct {
	parse.Field
	Cases    []string
	FuncName string
}

func writeRequired(f parse.Field) string {
	return fmt.Sprintf(`func %s(x *%s, vals []%s) {
	x.%s = vals[0]
}`, fmt.Sprintf("write%s", strings.Join(f.FieldNames, "")), f.Type, f.TypeName, strings.Join(f.FieldNames, "."))
}

func writeNested(f parse.Field) string {
	i := writeInput{
		Field:    f,
		FuncName: strings.Join(f.FieldNames, ""),
		Cases:    writeCases(f),
	}

	var buf bytes.Buffer
	err := writeTpl.Execute(&buf, i)
	if err != nil {
		log.Fatal(err) //TODO: return error
	}
	return string(buf.Bytes())
}

func writeCases(f parse.Field) []string {
	var out []string
	for def := 1; def <= defs(f); def++ {
		var v, ret string
		if def == defs(f) {
			v = `v := vals[0]
		`
			ret = `
	return true
	`
		}

		cs := fmt.Sprintf(`case %d:
	`, def)

		out = append(out, fmt.Sprintf(`%s%s%s%s`, cs, v, ifelse(0, def, f), ret))
	}
	return out
}

// return an if else block for the definition level
func ifelse(i, def int, f parse.Field) string {
	if i == recursions(def, f) {
		return ""
	}

	var stmt, brace, val, field, cmp string
	if i == 0 && defs(f) == 1 && f.Optionals[len(f.Optionals)-1] {
		return fmt.Sprintf(`x.%s = &v`, strings.Join(f.FieldNames, "."))
	} else if i == 0 {
		stmt = "if"
		brace = "}"
		field = fmt.Sprintf("x.%s", nilField(i, f))
		cmp = fmt.Sprintf(" x.%s == nil", nilField(i, f))
		ch := f.Child(defIndex(i, f))
		val = structs.Init(def, ch)
	} else if i > 0 && i < defs(f)-1 {
		stmt = " else if"
		brace = "}"
		cmp = fmt.Sprintf(" x.%s == nil", nilField(i, f))
		ch := f.Child(defIndex(i, f))
		val = structs.Init(def-i, ch)
		field = fmt.Sprintf("x.%s", nilField(i, f))
	} else {
		stmt = " else"
		val = "v"
		if f.Optionals[len(f.Optionals)-1] {
			val = "&v"
		}
		brace = "}"
		field = fmt.Sprintf("x.%s", strings.Join(f.FieldNames, "."))
	}

	return fmt.Sprintf(`%s%s {
	%s = %s
	%s%s`, stmt, cmp, field, val, brace, ifelse(i+1, def, f))
}

// recursions calculates the number of times ifelse should execute
func recursions(def int, f parse.Field) int {
	n := def
	if defs(f) == 1 {
		n++
	}
	return n
}

func nilField(i int, f parse.Field) string {
	var fields []string
	var count int
	for j, o := range f.Optionals {
		fields = append(fields, f.FieldNames[j])
		if o {
			count++
		}
		if count > i {
			break
		}
	}
	return strings.Join(fields, ".")
}

func defIndex(i int, f parse.Field) int {
	var count int
	for j, o := range f.Optionals {
		if o {
			count++
		}
		if count > i {
			return j
		}
	}
	return -1
}

// count the number of fields in the path that can be optional
func defs(f parse.Field) int {
	var out int
	for _, o := range f.Optionals {
		if o {
			out++
		}
	}
	return out
}

func pointer(i, n int, p string, levels []bool) string {
	if levels[n] && n < i {
		return p
	}
	return ""
}
