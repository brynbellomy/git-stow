package cli

import (
	"fmt"
	"strings"
)

// option is one command-line option, parsed the way git's parse-options does.
type option struct {
	short    byte
	long     string
	takesArg bool
	// negatable options also accept --no-<long>.
	negatable bool
	// set records the option; value is "" for options without an argument.
	set func(value string, negated bool) error
}

// parseOptions parses args as git's parse_options does: options and
// operands may be interleaved, short options bundle (-uk, -mmsg), long options
// may be abbreviated to a unique prefix and take values as --opt=v or --opt v.
// Everything after "--" is an operand; keepDashDash keeps the "--" itself.
func parseOptions(args []string, opts []option, keepDashDash bool) ([]string, error) {
	var operands []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			if keepDashDash {
				operands = append(operands, arg)
			}
			return append(operands, args[i+1:]...), nil
		case strings.HasPrefix(arg, "--"):
			consumed, err := parseLong(arg[2:], args[i+1:], opts)
			if err != nil {
				return nil, err
			}
			i += consumed
		case strings.HasPrefix(arg, "-") && arg != "-":
			consumed, err := parseShorts(arg[1:], args[i+1:], opts)
			if err != nil {
				return nil, err
			}
			i += consumed
		default:
			operands = append(operands, arg)
		}
	}
	return operands, nil
}

// parseLong handles one --name[=value] and returns how many following
// arguments it consumed as its value.
func parseLong(spec string, next []string, opts []option) (int, error) {
	name, value, hasValue := strings.Cut(spec, "=")
	o, negated, err := lookupLong(name, opts)
	if err != nil {
		return 0, err
	}
	switch {
	case !o.takesArg || negated:
		if hasValue {
			return 0, usageErrorf("option `%s' takes no value", name)
		}
		return 0, o.set("", negated)
	case hasValue:
		return 0, o.set(value, false)
	case len(next) > 0:
		return 1, o.set(next[0], false)
	default:
		return 0, usageErrorf("option `%s' requires a value", o.long)
	}
}

// lookupLong resolves an exact or uniquely abbreviated long option name,
// including the --no-<name> form of negatable options.
func lookupLong(name string, opts []option) (option, bool, error) {
	type candidate struct {
		o       option
		negated bool
	}
	var prefixed []candidate
	for _, o := range opts {
		forms := []candidate{{o, false}}
		if o.negatable {
			forms = append(forms, candidate{o, true})
		}
		for _, f := range forms {
			full := f.o.long
			if f.negated {
				full = "no-" + full
			}
			if name == full {
				return f.o, f.negated, nil
			}
			if strings.HasPrefix(full, name) {
				prefixed = append(prefixed, f)
			}
		}
	}
	if len(prefixed) == 1 {
		return prefixed[0].o, prefixed[0].negated, nil
	}
	if len(prefixed) > 1 {
		return option{}, false, usageErrorf("ambiguous option: %s", name)
	}
	return option{}, false, usageErrorf("unknown option `%s'", name)
}

// parseShorts handles one bundle of short options and returns how many
// following arguments it consumed as a value.
func parseShorts(bundle string, next []string, opts []option) (int, error) {
	for j := 0; j < len(bundle); j++ {
		o, ok := lookupShort(bundle[j], opts)
		if !ok {
			return 0, usageErrorf("unknown switch `%c'", bundle[j])
		}
		if !o.takesArg {
			if err := o.set("", false); err != nil {
				return 0, err
			}
			continue
		}
		if rest := bundle[j+1:]; rest != "" {
			return 0, o.set(rest, false)
		}
		if len(next) == 0 {
			return 0, usageErrorf("switch `%c' requires a value", o.short)
		}
		return 1, o.set(next[0], false)
	}
	return 0, nil
}

func lookupShort(c byte, opts []option) (option, bool) {
	for _, o := range opts {
		if o.short != 0 && o.short == c {
			return o, true
		}
	}
	return option{}, false
}

// flag returns an option setter for a boolean that --no-<name> clears.
func flag(p *bool) func(string, bool) error {
	return func(_ string, negated bool) error {
		*p = !negated
		return nil
	}
}

// usageError is a malformed command line: git prints "error: <msg>" and the
// usage, and exits 129.
type usageError struct {
	msg string
}

func (e *usageError) Error() string {
	return e.msg
}

func usageErrorf(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}
