package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"

	"neugram.io/ng/eval/gowrap"
	"neugram.io/ng/eval/gowrap/genwrap"
	_ "neugram.io/ng/eval/gowrap/wrapbuiltin"
	"neugram.io/ng/gotool"
)

func main() {
	env := &environment{
		values: map[string]*expression{
			"=": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					equal := true
					for i := 1; i < len(args); i++ {
						a1 := args[i-1]
						a2 := args[i]

						// TODO(robbiev) assuming integer
						equal = equal && *a1.atom.integer == *a2.atom.integer
					}
					return &expression{
						atom: &atom{boolean: &equal},
					}
				},
			},
			"+": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					var sum int
					for _, a := range args {
						// TODO(robbiev) assuming integer
						sum += *a.atom.integer
					}
					return &expression{
						atom: &atom{integer: &sum},
					}
				},
			},
			"*": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					sum := 1
					for _, a := range args {
						// TODO(robbiev) assuming integer
						sum *= *a.atom.integer
					}
					return &expression{
						atom: &atom{integer: &sum},
					}
				},
			},
			// TODO(robbiev) could be implement in lisp?
			// "do" evaluates expressions in order and returns the final one
			"do": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					return args[len(args)-1]
				},
			},
			"import": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					src, err := genwrap.GenGo("github.com/robbiev/hello", "main", false)
					if err != nil {
						panic(fmt.Errorf("plugin: wrapper gen failed for Go package %q: %v", "github.com/robbiev/hello", err))
					}
					if _, err := gotool.M.Create("github.com/robbiev/hello", src); err != nil {
						panic(err)
					}

					gowrap.Pkgs["hello"] = gowrap.Pkgs["github.com/robbiev/hello"]
					return nil
				},
			},
			">": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					result := *args[0].atom.integer > *args[1].atom.integer
					return &expression{
						atom: &atom{
							boolean: &result,
						},
					}
				},
			},
		},
	}

	var input io.Reader
	if len(os.Args) > 1 {
		input = strings.NewReader(os.Args[1] + "\n")
	} else {
		input = os.Stdin
	}

	scan := bufio.NewScanner(input)
	for scan.Scan() {
		text := scan.Text()
		fmt.Println("=>", text)
		tokens := tokenize(text)
		//fmt.Println(tokens)
		program, _, err := read(tokens)
		if err != nil {
			log.Fatal(err)
		}

		//printAST(program, 0)

		result := eval(env, program)

		var b bytes.Buffer
		printResult(result, &b)
		fmt.Println(b.String())
	}

	if err := scan.Err(); err != nil {
		log.Fatal(err)
	}
}

func printResult(expr *expression, buf *bytes.Buffer) {
	if expr == nil {
		buf.WriteString("nil")
		return
	}

	if expr.atom != nil {
		switch {
		case expr.atom.boolean != nil:
			buf.WriteString(fmt.Sprintf("%t", *expr.atom.boolean))
		case expr.atom.float != nil:
			buf.WriteString(fmt.Sprintf("%f", *expr.atom.float))
		case expr.atom.integer != nil:
			buf.WriteString(fmt.Sprintf("%d", *expr.atom.integer))
		case expr.atom.symbol != nil:
			buf.WriteString(*expr.atom.symbol)
		}

		return
	}

	if expr.gofunc != nil {
		buf.WriteString(fmt.Sprintf("gofunc-%p"))
		return
	}

	buf.WriteByte('(')
	for i, e := range expr.expressions {
		printResult(e, buf)
		if i < len(expr.expressions)-1 {
			buf.WriteByte(' ')
		}
	}
	buf.WriteByte(')')
}

func printAST(expr *expression, indent int) {
	if expr.atom != nil {
		fmt.Printf(strings.Repeat(" ", indent)+"atom: %+v\n", expr.atom)
	}
	if expr.expressions != nil {
		for _, s := range expr.expressions {
			printAST(s, indent+2)
		}
	}
}

func eval(env *environment, expr *expression) *expression {
	if expr.atom != nil {
		if expr.atom.symbol != nil {
			return env.lookup(*expr.atom.symbol)
		}

		// numeric constant
		return expr
	}

	actorAtom := expr.expressions[0].atom

	if actorAtom != nil && *actorAtom.symbol == "if" {
		test := expr.expressions[1]
		trueBranch := expr.expressions[2]
		falseBranch := expr.expressions[3]
		var branch *expression
		if *eval(env, test).atom.boolean {
			branch = trueBranch
		} else {
			branch = falseBranch
		}
		return eval(env, branch)
	} else if actorAtom != nil && *actorAtom.symbol == "first" {
		return eval(env, expr.expressions[1]).expressions[0]
	} else if actorAtom != nil && *actorAtom.symbol == "rest" {
		return &expression{
			expressions: eval(env, expr.expressions[1]).expressions[1:],
		}
	} else if actorAtom != nil && *actorAtom.symbol == "cons" {
		rest := eval(env, expr.expressions[2])
		return &expression{
			expressions: append([]*expression{eval(env, expr.expressions[1])}, rest.expressions...),
		}
	} else if actorAtom != nil && *actorAtom.symbol == "def" {
		env.values[*expr.expressions[1].atom.symbol] = eval(env, expr.expressions[2])
		// TODO(robbiev) anything to return?
	} else if actorAtom != nil && *actorAtom.symbol == "quote" {
		return expr.expressions[1]
	} else if actorAtom != nil && *actorAtom.symbol == "func" {
		params := expr.expressions[1]
		body := expr.expressions[2]
		return &expression{
			gofunc: func(parentEnv *environment, args []*expression) *expression {
				env := &environment{
					parent: parentEnv,
					values: map[string]*expression{},
				}

				if params.atom != nil && params.atom.symbol != nil {
					env.values[*params.atom.symbol] = &expression{
						expressions: args,
					}
				} else {
					for i, p := range params.expressions {
						env.values[*p.atom.symbol] = args[i]
					}
				}
				return eval(env, body)
			},
		}
	} else {
		proc := *eval(env, expr.expressions[0])
		var args []*expression
		for _, subj := range expr.expressions[1:] {
			args = append(args, eval(env, subj))
		}
		return proc.gofunc(env, args)
	}

	return nil
}

func tokenize(s string) []string {
	leftPad := strings.Replace(s, "(", " ( ", -1)
	rightPad := strings.Replace(leftPad, ")", " ) ", -1)
	return strings.Fields(rightPad)
}

func read(tokens []string) (*expression, []string, error) {
	if len(tokens) == 0 {
		return nil, nil, errors.New("unexpected EOF")
	}
	token, poptokens := tokens[0], tokens[1:]
	if token == "(" {
		var mainast expression
		for poptokens[0] != ")" {
			subast, ntokens, err := read(poptokens)
			if err != nil {
				// TODO(robbiev) better error handling
				return nil, nil, err
			}
			mainast.expressions = append(mainast.expressions, subast)
			poptokens = ntokens
		}
		poptokens = poptokens[1:] // pop off ")"

		return &mainast, poptokens, nil
	} else if token == ")" {
		return nil, nil, errors.New("unexpected )")
	} else {
		at, err := readAtom(token)
		if err != nil {
			// TODO(robbiev) better error handling
			return nil, nil, err
		}
		return &expression{atom: at}, poptokens, nil
	}
}

func readAtom(s string) (*atom, error) {
	if s == "true" || s == "false" {
		b := s == "true"
		return &atom{
			boolean: &b,
		}, nil
	}

	i, err := strconv.Atoi(s)
	if err == nil {
		return &atom{
			integer: &i,
		}, nil
	}

	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return &atom{
			float: &f,
		}, nil
	}

	return &atom{
		symbol: &s,
	}, nil
}

// only one field will be non-nil
type atom struct {
	integer *int
	boolean *bool
	float   *float64
	symbol  *string
}

// only one field will be non-nil
type expression struct {
	expressions []*expression
	atom        *atom

	// TODO neither an atom nor a list
	gofunc func(env *environment, args []*expression) *expression
}

type environment struct {
	values map[string]*expression
	parent *environment
}

func (e *environment) lookup(key string) *expression {
	if v, ok := e.values[key]; ok {
		return v
	}
	if e.parent != nil {
		return e.parent.lookup(key)
	}

	split := strings.Split(key, ".")
	pkg, fun := split[0], split[1]
	stdlib := gowrap.Pkgs
	stdlibPkg := stdlib[pkg]
	if stdlibPkg == nil {
		return nil
	}
	stdlibFun := stdlibPkg.Exports[fun]
	if !stdlibFun.IsValid() {
		return nil
	}
	if stdlibFun.Kind() != reflect.Func {
		panic(fmt.Sprintf("%s is not a function: %v", key, stdlibFun.Kind()))
	}
	return &expression{
		gofunc: func(env *environment, args []*expression) *expression {
			var reflectArgs []reflect.Value
			for _, a := range args {
				var v reflect.Value
				switch {
				case a.atom.integer != nil:
					v = reflect.ValueOf(*a.atom.integer)
				case a.atom.boolean != nil:
					v = reflect.ValueOf(*a.atom.boolean)
				case a.atom.float != nil:
					v = reflect.ValueOf(*a.atom.float)
				}
				reflectArgs = append(reflectArgs, v)
			}

			results := stdlibFun.Call(reflectArgs)

			var exprResults []*expression
			for _, r := range results {
				switch r.Kind() {
				case reflect.Int:
					i := int(r.Int())
					exprResults = append(exprResults, &expression{
						atom: &atom{
							integer: &i,
						},
					})
				case reflect.Bool:
					b := r.Bool()
					exprResults = append(exprResults, &expression{
						atom: &atom{
							boolean: &b,
						},
					})
				case reflect.Float64:
					f := r.Float()
					exprResults = append(exprResults, &expression{
						atom: &atom{
							float: &f,
						},
					})
				default:
					panic(fmt.Sprintf("%v has an unsupported type: %v", r, r.Kind()))
				}
			}

			if len(exprResults) == 0 {
				return nil
			}

			if len(exprResults) == 1 {
				return exprResults[0]
			}

			return &expression{
				expressions: exprResults,
			}
		},
	}
}
