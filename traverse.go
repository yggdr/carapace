package carapace

import (
	"strings"

	"github.com/rsteube/carapace/internal/common"
	"github.com/rsteube/carapace/internal/pflagfork"
	"github.com/spf13/cobra"
)

type InFlag struct {
	*pflagfork.Flag
	// currently consumed args since encountered flag
	Args []string
}

func (f InFlag) Consumes(arg string) bool {
	switch {
	case f.Flag == nil:
		return false
	case !f.TakesValue():
		return false
	case f.IsOptarg():
		return false
	case len(f.Args) == 0:
		return true
		// TODO another case that takes multiple (nargs) and arg is not a flag (breaking consumption chain)
	default:
		return false
	}
}

func traverse(c *cobra.Command, args []string) (Action, Context) {
	preInvoke(c, args)
	logger.Printf("traverse called for %#v with args %#v\n", c.Name(), args)

	inArgs := []string{} // args consumed by current command
	var inFlag *InFlag   // last encountered flag that still expects arguments
	fs := pflagfork.FlagSet{FlagSet: c.Flags()}

	context := NewContext(args)
loop:
	for i, arg := range context.Args {
		switch {
		// flag argument
		case inFlag != nil && inFlag.Consumes(arg):
			logger.Printf("arg %#v is a flag argument\n", arg)
			inArgs = append(inArgs, arg)
			inFlag.Args = append(inFlag.Args, arg)

			if !inFlag.Consumes("") {
				inFlag = nil // no more args expected
			}
			continue

		// dash
		case arg == "--":
			logger.Printf("arg %#v is dash\n", arg)
			inArgs = append(inArgs, args[i:]...)
			break loop

		// flag
		case strings.HasPrefix(arg, "-"):
			logger.Printf("arg %#v is a flag\n", arg)
			inFlag = &InFlag{
				Flag: fs.LookupArg(arg), // TODO can be nil
				Args: []string{},
			}
			inArgs = append(inArgs, arg)
			continue

		// subcommand
		case subcommand(c, arg) != nil:
			logger.Printf("arg %#v is a subcommand\n", arg)
			// TODO update args to parse (skip flag missing argument)
			logger.Printf("parsing flags for %#v with args %#v\n", c.Name(), inArgs)
			if err := c.ParseFlags(inArgs); err != nil {
				return ActionMessage(err.Error()), context
			}
			// TODO what if there is no next argument
			return traverse(subcommand(c, arg), args[i+1:])

		// positional
		default:
			logger.Printf("arg %#v is a positional\n", arg)
			inArgs = append(inArgs, arg)
		}
	}

	toParse := context.Args
	// TODO remove args that would case a parse error (flag witout value)
	// TODO add CallBackvalue to parsed ags if posix shorthand chain (skip last rune if it expects a value)
	if inFlag != nil && inFlag.Consumes("") {
		logger.Printf("removing arg %#v since it is a flag missing its argument\n", toParse[len(toParse)-1])
		toParse = toParse[:len(toParse)-1] // TODO nargs support
	} else if strings.HasPrefix(context.CallbackValue, "-") && (pflagfork.FlagSet{FlagSet: c.Flags()}).IsPosix() {
		logger.Printf("not removing args from %#v\n", toParse)
	}
	logger.Printf("inFlag %#v\n", inFlag)

	logger.Printf("parsing flags for %#v with args %#v\n", c.Name(), inArgs)
	if err := c.ParseFlags(toParse); err != nil {
		return ActionMessage(err.Error()), context
	}

	switch {
	// dash argument
	case common.IsDash(c):
		logger.Printf("completing dash for arg %#v\n", context.CallbackValue)
		context.Args = c.Flags().Args()[c.ArgsLenAtDash():]
		return storage.getPositional(c, len(context.Args)), context

	// flag argument
	case inFlag != nil && inFlag.Consumes(context.CallbackValue):
		logger.Printf("completing flag argument for arg %#v\n", context.CallbackValue)
		return storage.getFlag(c, inFlag.Name), context

	// flag
	case strings.HasPrefix(context.CallbackValue, "-"):
		if f := fs.LookupArg(context.CallbackValue); f != nil && f.IsOptarg() && strings.Contains(context.CallbackValue, "=") {
			logger.Printf("completing optional flag argument for arg %#v\n", context.CallbackValue)
			splitted := strings.SplitN(context.CallbackValue, "=", 2)
			context.CallbackValue = splitted[1]
			return storage.getFlag(c, f.Name).Prefix(splitted[0] + "="), context
		}
		logger.Printf("completing flags for arg %#v\n", context.CallbackValue)
		return actionFlags(c), context

	// positional or subcommand
	default:
		logger.Printf("completing positional and subcommands for arg %#v\n", context.CallbackValue)
		return Batch(
				storage.getPositional(c, len(c.Flags().Args())),
				actionSubcommands(c),
			).ToA(),
			context
	}
}

func subcommand(cmd *cobra.Command, arg string) *cobra.Command {
	if subcommand, _, _ := cmd.Find([]string{arg}); subcommand != cmd {
		return subcommand
	}
	return nil
}

func preInvoke(cmd *cobra.Command, args []string) {
	if subCmd := subcommand(cmd, "_carapace"); subCmd != nil && subCmd.PreRun != nil {
		subCmd.PreRun(cmd, args)
	}
}