package carapace

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/rsteube/carapace/internal/cache"
	"github.com/rsteube/carapace/internal/common"
	"github.com/rsteube/carapace/internal/lexer"
	pkgcache "github.com/rsteube/carapace/pkg/cache"
	"github.com/rsteube/carapace/pkg/style"
)

// Action indicates how to complete a flag or positional argument.
type Action struct {
	meta      common.Meta
	rawValues common.RawValues
	callback  CompletionCallback
}

// ActionMap maps Actions to an identifier.
type ActionMap map[string]Action

// CompletionCallback is executed during completion of associated flag or positional argument.
type CompletionCallback func(c Context) Action

// Cache cashes values of a CompletionCallback for given duration and keys.
func (a Action) Cache(timeout time.Duration, keys ...pkgcache.Key) Action {
	if a.callback != nil { // only relevant for callback actions
		cachedCallback := a.callback
		_, file, line, _ := runtime.Caller(1) // generate uid from wherever Cache() was called
		a.callback = func(c Context) Action {
			cacheFile, err := cache.File(file, line, keys...)
			if err != nil {
				return cachedCallback(c)
			}

			if cached, err := cache.Load(cacheFile, timeout); err == nil {
				return Action{meta: cached.Meta, rawValues: cached.Values}
			}

			invokedAction := (Action{callback: cachedCallback}).Invoke(c)
			if invokedAction.meta.Messages.IsEmpty() {
				if cacheFile, err := cache.File(file, line, keys...); err == nil { // regenerate as cache keys might have changed due to invocation
					_ = cache.Write(cacheFile, invokedAction.export())
				}
			}
			return invokedAction.ToA()
		}
	}
	return a
}

// Chdir changes the current working directory to the named directory for the duration of invocation.
func (a Action) Chdir(dir string) Action {
	return ActionCallback(func(c Context) Action {
		abs, err := c.Abs(dir)
		if err != nil {
			return ActionMessage(err.Error())
		}
		if info, err := os.Stat(abs); err != nil {
			return ActionMessage(err.Error())
		} else if !info.IsDir() {
			return ActionMessage("not a directory: %v", abs)
		}
		c.Dir = abs
		return a.Invoke(c).ToA()
	})
}

// Filter filters given values.
//
//	carapace.ActionValues("A", "B", "C").Filter([]string{"B"}) // ["A", "C"]
func (a Action) Filter(values []string) Action {
	return ActionCallback(func(c Context) Action {
		return a.Invoke(c).Filter(values).ToA()
	})
}

// Invoke executes the callback of an action if it exists (supports nesting).
func (a Action) Invoke(c Context) InvokedAction {
	if c.Args == nil {
		c.Args = []string{}
	}
	if c.Env == nil {
		c.Env = []string{}
	}
	if c.Parts == nil {
		c.Parts = []string{}
	}

	if a.rawValues == nil && a.callback != nil {
		result := a.callback(c).Invoke(c)
		result.meta.Merge(a.meta)
		return result
	}
	return InvokedAction{a}
}

// List wraps the Action in an ActionMultiParts with given divider.
func (a Action) List(divider string) Action {
	return ActionMultiParts(divider, func(c Context) Action {
		return a.Invoke(c).ToA().NoSpace()
	})
}

// MultiParts splits values of an Action by given dividers and completes each segment separately.
func (a Action) MultiParts(dividers ...string) Action {
	return ActionCallback(func(c Context) Action {
		return a.Invoke(c).ToMultiPartsA(dividers...)
	})
}

// NoSpace disables space suffix for given characters (or all if none are given).
func (a Action) NoSpace(suffixes ...rune) Action {
	return ActionCallback(func(c Context) Action {
		if len(suffixes) == 0 {
			a.meta.Nospace.Add('*')
		}
		a.meta.Nospace.Add(suffixes...)
		return a
	})
}

// Prefix adds a prefix to values (only the ones inserted, not the display values).
//
//	carapace.ActionValues("melon", "drop", "fall").Prefix("water")
func (a Action) Prefix(prefix string) Action {
	return ActionCallback(func(c Context) Action {
		switch {
		case strings.HasPrefix(c.Value, prefix):
			c.Value = strings.TrimPrefix(c.Value, prefix)
		case strings.HasPrefix(prefix, c.Value):
			c.Value = ""
		default:
			return ActionValues()
		}
		return a.Invoke(c).Prefix(prefix).ToA()
	})
}

// Retain retains given values.
//
//	carapace.ActionValues("A", "B", "C").Retain("A", "C") // ["A", "C"]
func (a Action) Retain(values ...string) Action {
	return ActionCallback(func(c Context) Action {
		return a.Invoke(c).Retain(values...).ToA()
	})
}

// Shift shifts positional arguments left `n` times.
func (a Action) Shift(n int) Action {
	return ActionCallback(func(c Context) Action {
		switch {
		case n < 0:
			return ActionMessage("invalid argument [ActionShift]: %v", n)
		case len(c.Args) < n:
			c.Args = []string{}
		default:
			c.Args = c.Args[n:]
		}
		return a.Invoke(c).ToA()
	})
}

// Split splits `Context.Value` using a shell lexer and replaces `Context.Args` with the tokens.
func (a Action) Split() Action {
	return ActionCallback(func(c Context) Action {
		tokenset, err := lexer.Split(c.Value)
		if err != nil {
			return ActionMessage(err.Error())
		}

		c.Args = tokenset.Tokens[:len(tokenset.Tokens)-1]
		c.Parts = []string{}
		c.Value = tokenset.Tokens[len(tokenset.Tokens)-1]
		invoked := a.Invoke(c)
		for index, value := range invoked.rawValues {
			if !invoked.meta.Nospace.Matches(value.Value) {
				switch tokenset.State {
				case lexer.OPEN_DOUBLE:
					invoked.rawValues[index].Value = fmt.Sprintf(`"%v" `, strings.Replace(value.Value, `"`, `\"`, -1))
				case lexer.OPEN_SINGLE:
					invoked.rawValues[index].Value = fmt.Sprintf(`'%v' `, strings.Replace(value.Value, `'`, `'"'"'`, -1))
				default:
					invoked.rawValues[index].Value = strings.Replace(value.Value, ` `, `\ `, -1) + ` `
				}
			}
		}
		invoked.Prefix(tokenset.Prefix)
		return invoked.ToA().NoSpace()
	})
}

// Style sets the style.
//
//	ActionValues("yes").Style(style.Green)
//	ActionValues("no").Style(style.Red)
func (a Action) Style(s string) Action {
	return a.StyleF(func(_ string, _ style.Context) string {
		return s
	})
}

// Style sets the style using a function.
//
//	ActionValues("dir/", "test.txt").StyleF(style.ForPathExt)
//	ActionValues("true", "false").StyleF(style.ForKeyword)
func (a Action) StyleF(f func(s string, sc style.Context) string) Action {
	return ActionCallback(func(c Context) Action {
		invoked := a.Invoke(c)
		for index, v := range invoked.rawValues {
			invoked.rawValues[index].Style = f(v.Value, c)
		}
		return invoked.ToA()
	})
}

// Style sets the style using a reference.
//
//	ActionValues("value").StyleR(&style.Carapace.Value)
//	ActionValues("description").StyleR(&style.Carapace.Value)
func (a Action) StyleR(s *string) Action {
	return ActionCallback(func(c Context) Action {
		if s != nil {
			return a.Style(*s)
		}
		return a
	})
}

// Suffix adds a suffx to values (only the ones inserted, not the display values).
//
//	carapace.ActionValues("apple", "melon", "orange").Suffix("juice")
func (a Action) Suffix(suffix string) Action {
	return ActionCallback(func(c Context) Action {
		return a.Invoke(c).Suffix(suffix).ToA()
	})
}

// Suppress suppresses specific error messages using regular expressions.
func (a Action) Suppress(expr ...string) Action {
	return ActionCallback(func(c Context) Action {
		invoked := a.Invoke(c)
		if err := invoked.meta.Messages.Suppress(expr...); err != nil {
			return ActionMessage(err.Error())
		}
		return invoked.ToA()
	})
}

// Tag sets the tag.
//
//	ActionValues("192.168.1.1", "127.0.0.1").Tag("interfaces").
func (a Action) Tag(tag string) Action {
	return a.TagF(func(value string) string {
		return tag
	})
}

// Tag sets the tag using a function.
//
//	ActionValues("192.168.1.1", "127.0.0.1").TagF(func(value string) string {
//		return "interfaces"
//	})
func (a Action) TagF(f func(s string) string) Action {
	return ActionCallback(func(c Context) Action {
		invoked := a.Invoke(c)
		for index, v := range invoked.rawValues {
			invoked.rawValues[index].Tag = f(v.Value)
		}
		return invoked.ToA()
	})
}

// Timeout sets the maximum duration an Action may take to invoke.
//
//	carapace.ActionCallback(func(c carapace.Context) carapace.Action {
//		time.Sleep(2*time.Second)
//		return carapace.ActionValues("done")
//	}).Timeout(1*time.Second, carapace.ActionMessage("timeout exceeded"))
func (a Action) Timeout(d time.Duration, alternative Action) Action {
	return ActionCallback(func(c Context) Action {
		currentChannel := make(chan string, 1)

		var result InvokedAction
		go func() {
			result = a.Invoke(c)
			currentChannel <- ""
		}()

		select {
		case <-currentChannel:
		case <-time.After(d):
			return alternative
		}
		return result.ToA()
	})
}

// UniqueList wraps the Action in an ActionMultiParts with given divider.
func (a Action) UniqueList(divider string) Action {
	return ActionMultiParts(divider, func(c Context) Action {
		return a.Invoke(c).Filter(c.Parts).ToA().NoSpace()
	})
}

// Usage sets the usage.
func (a Action) Usage(usage string, args ...interface{}) Action {
	return a.UsageF(func() string {
		return fmt.Sprintf(usage, args...)
	})
}

// Usage sets the usage using a function.
func (a Action) UsageF(f func() string) Action {
	return ActionCallback(func(c Context) Action {
		if usage := f(); usage != "" {
			a.meta.Usage = usage
		}
		return a
	})
}
