package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/robbiev/tipi/lexer"
	"neugram.io/ng/eval/gowrap"
	"neugram.io/ng/eval/gowrap/genwrap"
	_ "neugram.io/ng/eval/gowrap/wrapbuiltin"
	"neugram.io/ng/gotool"
)

var macros map[string]*expression = map[string]*expression{}

func main() {
	env := &environment{
		values: map[string]*expression{
			// TODO(robbiev): lex question marks
			"empty": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					result := len(args[0].expressions) == 0
					return &expression{
						atom: &atom{
							boolean: &result,
						},
					}
				},
			},
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
			"-": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					sum := *args[0].atom.integer
					for _, a := range args[1:] {
						// TODO(robbiev) assuming integer
						sum -= *a.atom.integer
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
			"first": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					return args[0].expressions[0]
				},
			},
			"apply": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					return args[0].gofunc(env, args[1].expressions)
				},
			},
			"rest": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					// TODO(robbiev): if-else need to for 'or' macro?
					if len(args[0].expressions) > 0 {
						return &expression{
							expressions: args[0].expressions[1:],
						}
					} else {
						return &expression{
							expressions: nil,
						}
					}
				},
			},
			"macro-expand": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					return expand(env, args[0])
				},
			},
			"import": &expression{
				gofunc: func(env *environment, args []*expression) *expression {
					path := *args[0].atom.str
					src, err := genwrap.GenGo(path, "main", false)
					if err != nil {
						panic(fmt.Errorf("plugin: wrapper gen failed for Go package %q: %v", path, err))
					}
					if _, err := gotool.M.Create(path, src); err != nil {
						panic(err)
					}

					pkg, err := gotool.M.ImportGo(path)
					if err != nil {
						panic(err)
					}
					gowrap.Pkgs[pkg.Name()] = gowrap.Pkgs[path]
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

	{
		b, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}

		var items []lexer.Item
		{
			l := lexer.Lex("", string(b))
			items = append(items, l.NextItem())
			for len(items) > 0 && items[len(items)-1].Type != lexer.ItemEOF {
				items = append(items, l.NextItem())
			}
			items = items[:len(items)-1] // Remove EOF
		}
		// fmt.Println(items)

		program, remaining, err := read(items)
		for err == nil {
			fmt.Println("=>", toString(program))

			expandedProgram := expand(env, program)

			result := eval(env, expandedProgram)

			fmt.Println(toString(result))

			if len(remaining) == 0 {
				break
			}

			program, remaining, err = read(remaining)
		}

		if err != nil {
			log.Fatal(err)
		}
	}
}

func toString(expr *expression) string {
	var b bytes.Buffer
	printResult(expr, &b)
	return b.String()
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
		case expr.atom.str != nil:
			buf.WriteString(*expr.atom.str)
		}

		return
	}

	if expr.gofunc != nil {
		buf.WriteString(fmt.Sprintf("gofunc-%p", expr.gofunc))
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
	// TODO(robbiev): only needed since expand() and def-macro returning nil there
	if expr == nil {
		return nil
	}

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
		// fmt.Println("proc", toString(&proc))
		// fmt.Println("proc args", toString(&expression{
		// 	expressions: args,
		// }))
		return proc.gofunc(env, args)
	}

	return nil
}

func tokenize(s string) []string {
	leftPad := strings.Replace(s, "(", " ( ", -1)
	rightPad := strings.Replace(leftPad, ")", " ) ", -1)
	return strings.Fields(rightPad)
}

func expandAll(env *environment, exprs []*expression) []*expression {
	var result []*expression
	for _, e := range exprs {
		result = append(result, expand(env, e))
	}
	return result
}

func expand(env *environment, expr *expression) *expression {
	if expr.atom != nil {
		return expr
	}

	actorAtom := expr.expressions[0].atom

	// if actorAtom != nil && actorAtom.symbol == nil {
	// 	fmt.Println("expand nil", toString(expr))
	// }
	if actorAtom != nil && *actorAtom.symbol == "quote" {
		return expr
	}

	if actorAtom != nil && *actorAtom.symbol == "def" {
		expr.expressions[2] = expand(env, expr.expressions[2])
		return expr
	}

	if actorAtom != nil && *actorAtom.symbol == "func" {
		expr.expressions[2] = expand(env, expr.expressions[2])
		return expr
	}

	if actorAtom != nil && *actorAtom.symbol == "def-macro" {
		// (def-macro my-name (func ...))
		expandedFunc := expand(env, expr.expressions[2])
		evaluatedFunc := eval(env, expandedFunc)
		macros[*expr.expressions[1].atom.symbol] = evaluatedFunc
		return nil
	}

	// calling a macro
	if actorAtom != nil && macros[*actorAtom.symbol] != nil {
		return expand(env, macros[*actorAtom.symbol].gofunc(env, expr.expressions[1:]))
	}

	return &expression{
		expressions: expandAll(env, expr.expressions),
	}
}

func read(tokens []lexer.Item) (*expression, []lexer.Item, error) {
	if len(tokens) == 0 {
		return nil, nil, errors.New("unexpected EOF")
	}
	token, poptokens := tokens[0], tokens[1:]
	if token.Type == lexer.ItemLeftParen {
		var mainast expression
		for poptokens[0].Type != lexer.ItemRightParen {
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
	} else if token.Type == lexer.ItemRightParen {
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

func readAtom(s lexer.Item) (*atom, error) {
	switch s.Type {
	case lexer.ItemString:
		// remove surrounding double quotes
		str := s.Value[1 : len(s.Value)-1]
		return &atom{
			str: &str,
		}, nil
	case lexer.ItemInt:
		i, _ := strconv.Atoi(s.Value)
		return &atom{
			integer: &i,
		}, nil
	case lexer.ItemFloat:
		f, _ := strconv.ParseFloat(s.Value, 64)
		return &atom{
			float: &f,
		}, nil
	case lexer.ItemBool:
		b := s.Value == "true"
		return &atom{
			boolean: &b,
		}, nil
	case lexer.ItemIdent:
		return &atom{
			symbol: &s.Value,
		}, nil
	}

	panic("unsupported")
}

// only one field will be non-nil
type atom struct {
	str     *string
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

	// fmt.Println("LOOKUP", key)

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
				case a.atom.str != nil:
					v = reflect.ValueOf(*a.atom.str)
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
				case reflect.String:
					s := r.String()
					exprResults = append(exprResults, &expression{
						atom: &atom{
							str: &s,
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
