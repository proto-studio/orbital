package console

import (
	"fmt"
	"regexp"
	"strings"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// errorToolsSource returns {classify, enter, leave, reset}.
// classify(value) is undefined when value is not an Error instance, otherwise
// a descriptor with the constructor name Node's inspect uses, the enumerable
// own keys, and whether non-enumerable cause / errors should be appended.
// enter/leave/reset track object identity for circular Errors.
//
// Detection is `instanceof Error`, which covers Error and its subclasses in
// this realm. A null-prototype error fails that test; formatting those needs
// V8's native error brand, which this binding does not expose.
const errorToolsSource = `(function () {
  var active = [];
  var refs = [];
  var next = 1;
  function find(list, v) {
    for (var i = 0; i < list.length; i++) {
      if (list[i] === v) return i;
    }
    return -1;
  }
  function classify(v) {
    if (v === null || (typeof v !== 'object' && typeof v !== 'function')) return undefined;
    try {
      if (!(v instanceof Error)) return undefined;
    } catch (e) {
      return undefined;
    }
    var ctor = null;
    var obj = v;
    while (obj) {
      var desc;
      try { desc = Object.getOwnPropertyDescriptor(obj, 'constructor'); }
      catch (e) { desc = undefined; }
      if (desc && typeof desc.value === 'function' && desc.value.name !== '') {
        try {
          if (v instanceof desc.value) { ctor = String(desc.value.name); break; }
        } catch (e2) {}
      }
      try { obj = Object.getPrototypeOf(obj); }
      catch (e) { break; }
    }
    var keys = [];
    try { keys = Object.keys(v); } catch (e) { keys = []; }
    var cause = false;
    try { cause = keys.indexOf('cause') === -1 && ('cause' in v); }
    catch (e) { cause = false; }
    var errors = false;
    try { errors = keys.indexOf('errors') === -1 && Array.isArray(v.errors); }
    catch (e) { errors = false; }
    return { ctor: ctor, keys: keys, cause: cause, errors: errors };
  }
  function enter(v) {
    var i = find(active, v);
    if (i !== -1) {
      var id = refs[i];
      if (!id) { id = next++; refs[i] = id; }
      return id;
    }
    active.push(v);
    refs.push(0);
    return 0;
  }
  function leave(v) {
    var i = find(active, v);
    if (i === -1) return 0;
    var id = refs[i] || 0;
    active.splice(i, 1);
    refs.splice(i, 1);
    return id;
  }
  function reset() {
    active = [];
    refs = [];
    next = 1;
  }
  return { classify: classify, enter: enter, leave: leave, reset: reset };
})()`

// nullProtoNameRe matches the header of a "regular" stack Node is willing to
// rewrite ("Error: ..." or "Error\n    at ...").
var nullProtoNameRe = regexp.MustCompile(`^([A-Z][A-Za-z0-9_ \[\]()-]*)(?::|\n {4}at)`)

var nullProtoErrRe = regexp.MustCompile(`^([A-Za-z0-9_-]*Error)$`)

type errorInfo struct {
	nullProto bool
	ctor      string
	keys      []string
	cause     bool
	errors    bool
}

type errorKey struct {
	name   string
	hidden bool // non-enumerable keys Node prints in brackets ([cause])
}

func (c *Console) installErrorClass(rt *runtime.Runtime) error {
	tools, err := rt.RunScript(errorToolsSource, "console_error_class.js")
	if err != nil {
		return err
	}
	if tools == nil || !tools.IsObject() {
		return fmt.Errorf("console: error classifier did not compile")
	}
	for _, name := range []string{"classify", "enter", "leave", "reset"} {
		fn, err := tools.Get(name)
		if err != nil || fn == nil || !fn.IsFunction() {
			return fmt.Errorf("console: error classifier missing %s", name)
		}
	}
	c.errorTools = tools
	return nil
}

func (c *Console) callErrorTool(name string, args ...*v8.Value) (*v8.Value, error) {
	if c.errorTools == nil {
		return nil, fmt.Errorf("console: error classifier is not installed")
	}
	fn, err := c.errorTools.Get(name)
	if err != nil || fn == nil {
		return nil, fmt.Errorf("console: error classifier missing %s", name)
	}
	return fn.Call(c.errorTools, args...)
}

// tryFormatError prints value like Node's util.inspect does for Error
// instances: the stack (name, message, frames), then extra own properties.
// The boolean is false when value is not an Error.
func (c *Console) tryFormatError(v *v8.Value, depth int) (string, bool) {
	if c.skipErrorFormat || c.errorTools == nil || v == nil {
		return "", false
	}

	info, ok := c.classifyError(v)
	if !ok {
		return "", false
	}

	top := !c.errorTracking
	if top {
		c.errorTracking = true
		_, _ = c.callErrorTool("reset")
		defer func() {
			c.errorTracking = false
			_, _ = c.callErrorTool("reset")
		}()
	}

	entered, err := c.callErrorTool("enter", v)
	if err != nil || entered == nil {
		return c.renderError(v, info, depth), true
	}
	if id := int(entered.Number()); id > 0 {
		return fmt.Sprintf("[Circular *%d]", id), true
	}

	rendered := c.renderError(v, info, depth)
	if left, err := c.callErrorTool("leave", v); err == nil && left != nil {
		if ref := int(left.Number()); ref > 0 {
			rendered = fmt.Sprintf("<ref *%d> %s", ref, rendered)
		}
	}
	return rendered, true
}

func (c *Console) classifyError(v *v8.Value) (errorInfo, bool) {
	if !v.IsObject() || v.IsArray() || v.IsFunction() {
		return errorInfo{}, false
	}
	res, err := c.callErrorTool("classify", v)
	if err != nil || res == nil || res.IsUndefined() || res.IsNull() || !res.IsObject() {
		return errorInfo{}, false
	}

	info := errorInfo{}
	ctorVal, _ := res.Get("ctor")
	switch {
	case ctorVal == nil || ctorVal.IsNull() || ctorVal.IsUndefined():
		info.nullProto = true
	default:
		info.ctor = ctorVal.String()
	}

	if keysVal, _ := res.Get("keys"); keysVal != nil && keysVal.IsArray() {
		n := keysVal.ArrayLength()
		info.keys = make([]string, 0, n)
		for i := 0; i < n; i++ {
			k, err := keysVal.GetIndex(i)
			if err != nil || k == nil {
				continue
			}
			info.keys = append(info.keys, k.String())
		}
	}
	if b, _ := res.Get("cause"); b != nil && b.Boolean() {
		info.cause = true
	}
	if b, _ := res.Get("errors"); b != nil && b.Boolean() {
		info.errors = true
	}
	return info, true
}

func (c *Console) renderError(v *v8.Value, info errorInfo, depth int) string {
	name := "Error"
	if nv, err := v.Get("name"); err == nil && nv != nil && !nv.IsUndefined() && !nv.IsNull() {
		name = nv.String()
	}
	message := ""
	if mv, err := v.Get("message"); err == nil && mv != nil && !mv.IsUndefined() && !mv.IsNull() {
		message = mv.String()
	}

	rawStack := ""
	if sv, err := v.Get("stack"); err == nil && sv != nil && !sv.IsUndefined() && !sv.IsNull() {
		rawStack = sv.String()
	}
	stack := rawStack
	if stack == "" {
		stack = errorToString(name, message)
	}

	keys := c.filterErrorKeys(v, info.keys, rawStack)
	if info.cause {
		keys = append(keys, errorKey{name: "cause", hidden: true})
	}
	if info.errors {
		keys = append(keys, errorKey{name: "errors", hidden: true})
	}

	stack = improveStack(stack, name, info.ctor, info.nullProto)
	stack = c.applyCauseFrames(stack, v, message)

	// Past the inspect depth, Node still shows the stack but not further
	// properties of an Error that is only reached through nesting.
	if depth <= 0 {
		return stack
	}
	if len(keys) == 0 {
		return stack
	}

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		val, err := v.Get(key.name)
		var valStr string
		if err != nil || val == nil {
			valStr = c.formatter.FormatValue("undefined", runtime.TypeUndefined)
		} else {
			// Pass depth through. formatValueQuotedWithDepth decrements once
			// for arrays and objects, and an AggregateError's `errors` array
			// still needs to show its elements (Node's default depth is 2).
			valStr = c.formatValueQuotedWithDepth(val, depth)
		}
		label := key.name
		if key.hidden {
			label = "[" + key.name + "]"
		}
		parts = append(parts, label+": "+valStr)
	}

	if strings.Contains(stack, "\n") || partsHaveNewline(parts) {
		return stack + " " + multilineBraces("{", "}", parts)
	}
	return stack + " { " + strings.Join(parts, ", ") + " }"
}

// filterErrorKeys drops enumerable name/message/stack when that text is already
// part of the stack, matching Node's removeDuplicateErrorKeys.
func (c *Console) filterErrorKeys(v *v8.Value, keys []string, stack string) []errorKey {
	drop := map[string]bool{}
	if stack != "" {
		for _, name := range []string{"name", "message", "stack"} {
			val, err := v.Get(name)
			if err != nil || val == nil || val.IsUndefined() || val.IsNull() {
				continue
			}
			s := val.String()
			if s != "" && strings.Contains(stack, s) {
				drop[name] = true
			}
		}
	}
	out := make([]errorKey, 0, len(keys))
	for _, key := range keys {
		if drop[key] {
			continue
		}
		out = append(out, errorKey{name: key})
	}
	return out
}

// applyCauseFrames rewrites a run of frames shared with error.cause the way
// Node does ("... N lines matching cause stack trace ...").
func (c *Console) applyCauseFrames(stack string, v *v8.Value, message string) string {
	pos := -1
	if message != "" {
		if i := strings.Index(stack, message); i >= 0 {
			pos = i + len(message)
		}
	}
	searchFrom := 0
	if pos > 0 {
		searchFrom = pos
	}
	rel := strings.Index(stack[searchFrom:], "\n    at")
	if rel < 0 {
		return "[" + stack + "]"
	}
	stackStart := searchFrom + rel
	header := stack[:stackStart]
	frames := strings.Split(stack[stackStart+1:], "\n")
	frames = c.collapseCauseFrames(frames, v)
	return header + "\n" + strings.Join(frames, "\n")
}

func (c *Console) collapseCauseFrames(frames []string, v *v8.Value) []string {
	cause, err := v.Get("cause")
	if err != nil || cause == nil || !cause.IsObject() {
		return frames
	}
	// Only native-looking causes (Error instances) participate. classifyError
	// is cheap relative to walking frames and keeps plain-object causes out.
	if _, ok := c.classifyError(cause); !ok {
		return frames
	}
	sv, err := cause.Get("stack")
	if err != nil || sv == nil || sv.IsUndefined() || sv.IsNull() {
		return frames
	}
	causeStack := sv.String()
	idx := strings.Index(causeStack, "\n    at")
	if idx < 0 {
		return frames
	}
	causeFrames := strings.Split(causeStack[idx+1:], "\n")
	length, offset := identicalSequenceRange(frames, causeFrames)
	if length == 0 {
		return frames
	}
	skipped := length - 2
	if skipped <= 0 || offset+1+skipped > len(frames) {
		return frames
	}
	msg := fmt.Sprintf("    ... %d lines matching cause stack trace ...", skipped)
	out := make([]string, 0, len(frames)-skipped+1)
	out = append(out, frames[:offset+1]...)
	out = append(out, msg)
	out = append(out, frames[offset+1+skipped:]...)
	return out
}

func errorToString(name, message string) string {
	if name == "" {
		name = "Error"
	}
	if message == "" {
		return name
	}
	return name + ": " + message
}

// improveStack rewrites the stack header when the constructor name and
// err.name disagree (class MyErr extends Error => "MyErr [Error]: ..."),
// matching Node's inspect.improveStack for an empty toStringTag.
func improveStack(stack, name, ctor string, nullProto bool) string {
	length := len(name)
	regular := strings.HasSuffix(name, "Error") &&
		strings.HasPrefix(stack, name) &&
		(len(stack) == length || (length < len(stack) && (stack[length] == ':' || stack[length] == '\n')))
	if !nullProto && !regular {
		return stack
	}

	fallback := "Error"
	if nullProto {
		var matched bool
		fallback, length, matched = nullProtoFallback(stack)
		if !matched {
			length = 0
			fallback = "Error"
		}
	}
	prefix := errorPrefix(ctor, fallback, nullProto)
	if name == prefix {
		return stack
	}
	if strings.Contains(prefix, name) {
		if length == 0 {
			return prefix + ": " + stack
		}
		if length > len(stack) {
			length = len(stack)
		}
		return prefix + stack[length:]
	}
	if length > len(stack) {
		length = len(stack)
	}
	return prefix + " [" + name + "]" + stack[length:]
}

func errorPrefix(ctor, fallback string, nullProto bool) string {
	if nullProto {
		if fallback == "" {
			fallback = "Error"
		}
		return "[" + fallback + ": null prototype]"
	}
	if ctor == "" {
		return fallback
	}
	return ctor
}

func nullProtoFallback(stack string) (name string, length int, ok bool) {
	if m := nullProtoNameRe.FindStringSubmatch(stack); m != nil {
		return m[1], len(m[1]), true
	}
	if m := nullProtoErrRe.FindStringSubmatch(stack); m != nil {
		return m[1], len(m[1]), true
	}
	return "", 0, false
}

// identicalSequenceRange finds a run of more than three identical lines shared
// by a and b. length is the run length and offset is where it starts in a.
func identicalSequenceRange(a, b []string) (length, offset int) {
	for i := 0; i < len(a)-3; i++ {
		pos := indexString(b, a[i])
		if pos < 0 {
			continue
		}
		rest := len(b) - pos
		if rest <= 3 {
			continue
		}
		n := 1
		maxLen := len(a) - i
		if rest < maxLen {
			maxLen = rest
		}
		for maxLen > n && a[i+n] == b[pos+n] {
			n++
		}
		if n > 3 {
			return n, i
		}
	}
	return 0, 0
}

func indexString(items []string, target string) int {
	for i, item := range items {
		if item == target {
			return i
		}
	}
	return -1
}

func partsHaveNewline(parts []string) bool {
	for _, p := range parts {
		if strings.Contains(p, "\n") {
			return true
		}
	}
	return false
}

// multilineBraces renders a collection the way Node does once a value contains
// a newline (an Error stack, for example): one entry per line, continuation
// lines indented two more spaces.
func multilineBraces(open, close string, parts []string) string {
	lines := make([]string, len(parts))
	for i, p := range parts {
		lines[i] = indentNewlines(p, 2)
	}
	return open + "\n  " + strings.Join(lines, ",\n  ") + "\n" + close
}

func indentNewlines(s string, n int) string {
	if n <= 0 || !strings.Contains(s, "\n") {
		return s
	}
	return strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", n))
}
