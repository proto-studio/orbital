package main

import (
	"errors"
	"strings"
)

// nodeOptKind is how an option in NODE_OPTIONS takes a value.
type nodeOptKind int

const (
	optBoolean nodeOptKind = iota // --flag, negated as --no-flag; a "=value" is ignored
	optValue                      // --flag=value or --flag value
	optV8                         // a V8 flag: --flag, --no-flag or --flag=value
	optNoOp                       // accepted and ignored
)

// nodeOptions is what NODE_OPTIONS sets among the options Orbital implements.
type nodeOptions struct {
	preload   []string // --require/-r and --import, in order, as argv tokens
	title     string
	inputType string
	// noWarnings is the final --[no-]warnings state, if either was given.
	noWarnings *bool
	// unhandledRejections is the last --unhandled-rejections value.
	unhandledRejections string
}

// args returns the options as argv tokens for Orbital's flag parser, which
// handles them before the command line's own (so those override scalar ones).
func (o *nodeOptions) args() []string {
	out := append([]string(nil), o.preload...)
	if o.title != "" {
		out = append(out, "--title", o.title)
	}
	if o.inputType != "" {
		out = append(out, "--input-type", o.inputType)
	}
	if o.noWarnings != nil && *o.noWarnings {
		out = append(out, "--no-warnings")
	}
	if o.unhandledRejections != "" {
		out = append(out, "--unhandled-rejections", o.unhandledRejections)
	}
	return out
}

// splitNodeOptions splits NODE_OPTIONS into arguments the way Node does: on
// spaces, with double quotes grouping and a backslash escaping the next
// character inside quotes.
func splitNodeOptions(s string) ([]string, error) {
	var out [][]byte
	inString, startNew := false, true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && inString:
			if i+1 == len(s) {
				return nil, errors.New("invalid value for NODE_OPTIONS (invalid escape)")
			}
			i++
			c = s[i]
		case c == ' ' && !inString:
			startNew = true
			continue
		case c == '"':
			inString = !inString
			continue
		}
		// Bytes, not runes: a multi-byte UTF-8 character passes through whole.
		if startNew {
			out = append(out, []byte{c})
			startNew = false
		} else {
			out[len(out)-1] = append(out[len(out)-1], c)
		}
	}
	if inString {
		return nil, errors.New("invalid value for NODE_OPTIONS (unterminated string)")
	}
	args := make([]string, len(out))
	for i, a := range out {
		args[i] = string(a)
	}
	return args, nil
}

// parseNodeOptions checks NODE_OPTIONS as Node 20.18.1 does, rejecting what Node
// rejects with Node's message, and collects the options Orbital implements. The
// rest of what Node accepts there is accepted and has no effect.
func parseNodeOptions(s string) (*nodeOptions, error) {
	args, err := splitNodeOptions(s)
	if err != nil {
		return nil, err
	}
	opts := &nodeOptions{}
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		// Node stops at the first argument that isn't an option.
		if len(arg) <= 1 || arg[0] != '-' {
			break
		}
		if arg == "--" {
			return nil, errors.New("-- is not allowed in NODE_OPTIONS")
		}
		name, value, hasValue := arg, "", false
		if strings.HasPrefix(arg, "--") {
			if eq := strings.IndexByte(arg, '='); eq >= 0 {
				name, value, hasValue = arg[:eq], arg[eq+1:], true
			}
			name = "--" + strings.ReplaceAll(name[2:], "_", "-")
		}
		// How Node names the option in its errors.
		shown := name
		if hasValue {
			shown += "="
		}

		// Expand aliases (-r, -C, --loader, --inspect=, ...); extra words become
		// the following arguments.
		for {
			exp, ok := nodeOptionAliases[name+"="]
			if !ok || !hasValue {
				exp, ok = nodeOptionAliases[name]
			}
			if !ok || len(exp) == 0 || exp[0] == name {
				break
			}
			name = exp[0]
			args = append(append([]string(nil), exp[1:]...), args...)
		}

		negated := false
		kind, known := nodeEnvOptions[name]
		if !known && strings.HasPrefix(name, "--no-") {
			kind, known = nodeEnvOptions["--"+name[5:]]
			if known {
				negated = true
				name = "--" + name[5:]
			}
		}
		if !known {
			return nil, errors.New(shown + " is not allowed in NODE_OPTIONS")
		}
		if negated && kind != optBoolean && kind != optV8 {
			return nil, errors.New(shown + " is an invalid negation because it is not a boolean option")
		}
		if kind == optValue {
			if !hasValue {
				// The next argument is the value, unless it is another option.
				if len(args) == 0 || strings.HasPrefix(args[0], "-") {
					return nil, errors.New(shown + " requires an argument")
				}
				value, args = args[0], args[1:]
			} else if value == "" {
				return nil, errors.New(shown + " requires an argument")
			}
		}

		switch name {
		case "--require", "--import":
			opts.preload = append(opts.preload, name, value)
		case "--title":
			opts.title = value
		case "--input-type":
			opts.inputType = value
		case "--warnings":
			off := negated
			opts.noWarnings = &off
		case "--unhandled-rejections":
			opts.unhandledRejections = value
		}
	}
	// Like Node, the value that counts is the last one.
	if opts.inputType != "" && opts.inputType != "module" && opts.inputType != "commonjs" {
		return nil, errors.New(`--input-type must be "module" or "commonjs"`)
	}
	return opts, nil
}
