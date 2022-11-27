package carapace

import (
	"fmt"
	"regexp"
	"runtime"
	"time"

	"github.com/rsteube/carapace/internal/cache"
	"github.com/rsteube/carapace/internal/common"
	pkgcache "github.com/rsteube/carapace/pkg/cache"
)

// Action indicates how to complete a flag or positional argument.
type Action struct {
	rawValues []common.RawValue
	callback  CompletionCallback
	nospace   common.SuffixMatcher
	skipcache bool
	usage     string
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
			if cacheFile, err := cache.File(file, line, keys...); err == nil {
				if rawValues, err := cache.Load(cacheFile, timeout); err == nil {
					return actionRawValues(rawValues...)
				}
				invokedAction := (Action{callback: cachedCallback}).Invoke(c)
				if !invokedAction.skipcache {
					_ = cache.Write(cacheFile, invokedAction.rawValues)
				}
				return invokedAction.ToA()
			}
			return cachedCallback(c)
		}
	}
	return a
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
	return InvokedAction{a.nestedAction(c, 10)}
}

func (a Action) nestedAction(c Context, maxDepth int) Action {
	if maxDepth < 0 {
		return ActionMessage("maximum recursion depth exceeded")
	}
	if a.rawValues == nil && a.callback != nil {
		return a.callback(c).nestedAction(c, maxDepth-1).noSpace(string(a.nospace)).skipCache(a.skipcache).withUsage(a.usage)
	}
	return a
}

// NoSpace disables space suffix for given characters (or all if none are given).
func (a Action) NoSpace(suffixes ...rune) Action {
	return ActionCallback(func(c Context) Action {
		if len(suffixes) == 0 {
			return a.noSpace("*")
		}
		return a.noSpace(string(suffixes))
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
		return a.withUsage(f())
	})
}

// Style sets the style
//
//	ActionValues("yes").Style(style.Green)
//	ActionValues("no").Style(style.Red)
func (a Action) Style(style string) Action {
	return a.StyleF(func(s string) string {
		return style
	})
}

// Style sets the style using a reference
//
//	ActionValues("value").StyleR(&style.Carapace.Value)
//	ActionValues("description").StyleR(&style.Carapace.Value)
func (a Action) StyleR(style *string) Action {
	return ActionCallback(func(c Context) Action {
		if style != nil {
			return a.Style(*style)
		}
		return a
	})
}

// Style sets the style using a function
//
//	ActionValues("dir/", "test.txt").StyleF(style.ForPathExt)
//	ActionValues("true", "false").StyleF(style.ForKeyword)
func (a Action) StyleF(f func(s string) string) Action {
	return ActionCallback(func(c Context) Action {
		invoked := a.Invoke(c)
		for index, v := range invoked.rawValues {
			if !v.IsMessage() {
				invoked.rawValues[index].Style = f(v.Value)
			}
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
func (a Action) TagF(f func(value string) string) Action {
	return ActionCallback(func(c Context) Action {
		invoked := a.Invoke(c)
		for index, v := range invoked.rawValues {
			if !v.IsMessage() {
				invoked.rawValues[index].Tag = f(v.Value)
			}
		}
		return invoked.ToA()
	})
}

// Chdir changes the current working directory to the named directory during invocation.
func (a Action) Chdir(dir string) Action {
	return ActionCallback(func(c Context) Action {
		abs, err := c.Abs(dir)
		if err != nil {
			return ActionMessage(err.Error())
		}
		c.Dir = abs
		return a.Invoke(c).ToA()
	})
}

// Suppress suppresses specific error messages using regular expressions.
func (a Action) Suppress(expr ...string) Action {
	return ActionCallback(func(c Context) Action {
		invoked := a.Invoke(c)
		filter := false
		for _, rawValue := range invoked.rawValues {
			if rawValue.IsMessage() && rawValue.Description != "" {
				for _, e := range expr {
					r, err := regexp.Compile(e)
					if err != nil {
						return ActionMessage(err.Error())
					}
					if r.MatchString(rawValue.Description) {
						filter = true
						break
					}
				}
			}
		}

		if filter {
			filtered := make([]common.RawValue, 0)
			for _, r := range invoked.rawValues {
				if !r.IsMessage() {
					filtered = append(filtered, r)
				}
			}
			invoked.rawValues = filtered
		}
		return invoked.ToA()
	})
}

// List wraps the Action in an ActionMultiParts with given divider.
func (a Action) List(divider string) Action {
	return ActionMultiParts(divider, func(c Context) Action {
		return a.Invoke(c).ToA().NoSpace()
	})
}

// UniqueList wraps the Action in an ActionMultiParts with given divider.
func (a Action) UniqueList(divider string) Action {
	return ActionMultiParts(divider, func(c Context) Action {
		return a.Invoke(c).Filter(c.Parts).ToA().NoSpace()
	})
}

func (a Action) noSpace(suffixes string) Action {
	a.nospace = a.nospace.Add(suffixes)
	return a
}

func (a Action) skipCache(state bool) Action {
	a.skipcache = a.skipcache || state
	return a
}

func (a Action) withUsage(usage string) Action {
	if usage != "" {
		a.usage = usage
	}
	return a
}
