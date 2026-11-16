package runtime

import (
	"hash/fnv"
	"strings"

	"proto.zip/studio/orbital/pkg/v8"
)

// The line printed above a fatal exception is read from the text the runtime compiled,
// never from a file: a stack is only a string the thrown value carries, and a file can
// change after its script was compiled. The runtime keeps that text by the name stack
// frames use. Modules keep theirs for as long as the runtime lives, as V8 keeps their
// code; scripts run through Run keep theirs while among the most recent keptScriptLimit.
// Everything kept, names included, is bounded by keptBytesLimit and keptNamesLimit.

// keptScriptLimit is how many scripts run through Run keep their text.
const keptScriptLimit = 16

// keptBytesLimit bounds the text and names kept in all: past it, new text isn't kept, and
// the header is left out for it.
const keptBytesLimit = 64 << 20

// keptNamesLimit bounds how many names are recorded at all. A name past it keeps no text,
// so its header is left out rather than guessed.
const keptNamesLimit = 1 << 16

// keptEvictedLimit is how many scripts whose text was dropped keep their name and hash.
const keptEvictedLimit = 4096

type keptSource struct {
	text      string
	hasText   bool
	ambiguous bool        // the name was used again for different text
	module    bool        // kept for the runtime's life
	hash      uint64      // of the text, kept after the text itself is dropped
	nul       string      // the character standing for NUL in text, or ""
	offset    int         // wrapper characters before the source on the first line
	script    *keptScript // the script it belongs to while kept as one
}

// keptScript is one script run through Run: its origin, and its sourceURL name if it has
// another, age together.
type keptScript struct {
	names []string
}

func sourceHash(text string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return h.Sum64()
}

// entry is the record for name, created within the bounds, or nil, with the map's own
// copy of the name, so a record never holds a caller's string.
func (rt *Runtime) entry(name string, hash uint64) (*keptSource, string) {
	if kept, ok := rt.keptSources[name]; ok {
		return kept, name
	}
	if rt.keptSources == nil {
		rt.keptSources = make(map[string]*keptSource)
	}
	if len(rt.keptSources) >= keptNamesLimit || rt.keptBytes+len(name) > keptBytesLimit {
		return nil, name
	}
	name = strings.Clone(name)
	kept := &keptSource{hash: hash}
	rt.keptSources[name] = kept
	rt.keptBytes += len(name)
	return kept, name
}

// matches reports whether kept can take text with hash. Functions from an earlier script
// under the same name can still run, and which text their frames came from can't be told,
// so a name reused for different text keeps none, and the header is left out.
func (rt *Runtime) matches(kept *keptSource, name string, hash uint64) bool {
	if !kept.ambiguous && kept.hash == hash {
		return true
	}
	rt.detach(kept, name)
	rt.dropText(kept)
	kept.ambiguous = true
	kept.offset = 0
	return false
}

func (rt *Runtime) setText(kept *keptSource, name, text string) bool {
	if kept.hasText {
		return true
	}
	if rt.keptBytes+len(text) > keptBytesLimit {
		return false
	}
	kept.text, kept.hasText = text, true
	rt.keptBytes += len(text)
	rt.removeName(&rt.keptEvicted, name)
	return true
}

func (rt *Runtime) dropText(kept *keptSource) {
	if kept.hasText {
		rt.keptBytes -= len(kept.text)
	}
	kept.text, kept.hasText = "", false
}

// detach takes name out of the script it was kept for.
func (rt *Runtime) detach(kept *keptSource, name string) {
	rec := kept.script
	if rec == nil {
		return
	}
	kept.script = nil
	rt.removeName(&rec.names, name)
	if len(rec.names) == 0 {
		for i, r := range rt.keptScripts {
			if r == rec {
				rt.keptScripts = append(rt.keptScripts[:i], rt.keptScripts[i+1:]...)
				break
			}
		}
	}
}

func (rt *Runtime) removeName(list *[]string, name string) {
	for i, n := range *list {
		if n == name {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return
		}
	}
}

func uniqueNames(names []string) []string {
	out := names[:0:0]
	for _, n := range names {
		if n == "" {
			continue
		}
		dup := false
		for _, o := range out {
			dup = dup || o == n
		}
		if !dup {
			out = append(out, n)
		}
	}
	return out
}

// keepScript keeps the text of a script run unwrapped, under each of its names.
func (rt *Runtime) keepScript(text string, names ...string) {
	hash := sourceHash(text)
	rec := &keptScript{}
	for _, name := range uniqueNames(names) {
		kept, key := rt.entry(name, hash)
		if kept == nil || !rt.matches(kept, name, hash) {
			continue
		}
		// It runs unwrapped, so a wrapper offset recorded for the name no longer applies.
		kept.offset = 0
		if kept.module {
			continue
		}
		rt.detach(kept, name)
		if !rt.setText(kept, name, text) {
			continue
		}
		kept.script = rec
		rec.names = append(rec.names, key)
	}
	if len(rec.names) == 0 {
		return
	}
	rt.keptScripts = append(rt.keptScripts, rec)
	for len(rt.keptScripts) > keptScriptLimit {
		old := rt.keptScripts[0]
		rt.keptScripts = rt.keptScripts[1:]
		for _, name := range old.names {
			kept, ok := rt.keptSources[name]
			if !ok || kept.script != old {
				continue
			}
			kept.script = nil
			rt.dropText(kept)
			// Its name and hash stay a while, so a reuse with other text is still seen.
			rt.keptEvicted = append(rt.keptEvicted, name)
		}
		for len(rt.keptEvicted) > keptEvictedLimit {
			gone := rt.keptEvicted[0]
			rt.keptEvicted = rt.keptEvicted[1:]
			if g, ok := rt.keptSources[gone]; ok && !g.hasText && !g.module && g.script == nil {
				delete(rt.keptSources, gone)
				rt.keptBytes -= len(gone)
			}
		}
	}
}

// keepModule keeps a module's text, as compiled, under each of its names for the
// runtime's life. nul is the character standing for NUL in text, or "".
func (rt *Runtime) keepModule(text, nul string, names ...string) {
	hash := sourceHash(text)
	for _, name := range uniqueNames(names) {
		kept, _ := rt.entry(name, hash)
		if kept == nil || !rt.matches(kept, name, hash) {
			continue
		}
		rt.detach(kept, name)
		if !rt.setText(kept, name, text) {
			continue
		}
		kept.module = true
		kept.nul = strings.Clone(nul)
	}
}

// forgetModule removes what keepModule kept for a module that then failed to compile: no
// function of it can run, so no frame will need it, and a corrected module under the same
// name isn't taken for a different one.
func (rt *Runtime) forgetModule(text string, names ...string) {
	hash := sourceHash(text)
	for _, name := range uniqueNames(names) {
		kept, ok := rt.keptSources[name]
		if !ok || kept.ambiguous || kept.hash != hash {
			continue
		}
		rt.detach(kept, name)
		rt.dropText(kept)
		rt.removeName(&rt.keptEvicted, name)
		delete(rt.keptSources, name)
		rt.keptBytes -= len(name)
	}
}

// keptSourceFunc is native.keptSource(name): the text kept for name, false if the name
// was reused for different text, or null.
func (rt *Runtime) keptSourceFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return rt.context.Null()
	}
	kept, ok := rt.keptSources[args[0].String()]
	if !ok {
		return rt.context.Null()
	}
	if kept.ambiguous {
		return rt.context.False()
	}
	if !kept.hasText {
		return rt.context.Null()
	}
	val, err := rt.context.NewString(kept.text)
	if err != nil {
		return rt.context.Null()
	}
	return val
}

// registerSource keeps a script run through Run, under its origin and the name a
// sourceURL directive gives it.
func (rt *Runtime) registerSource(source, origin string) {
	rt.keepScript(source, origin, sourceURL(source))
}

// sourceURL is the name a "//# sourceURL=" directive gives a script, as V8 reads the last
// one: "//", then "#" or "@", then spaces or tabs, then the directive. Otherwise "".
func sourceURL(source string) string {
	for end := len(source); end > 0; {
		i := strings.LastIndex(source[:end], "sourceURL=")
		if i < 0 {
			return ""
		}
		end = i
		j := i
		for j > 0 && (source[j-1] == ' ' || source[j-1] == '\t') {
			j--
		}
		if j == i || j < 3 || (source[j-1] != '#' && source[j-1] != '@') || source[j-3:j-1] != "//" {
			continue
		}
		rest := source[i+len("sourceURL="):]
		if k := strings.IndexAny(rest, " \t\r\n"); k != -1 {
			rest = rest[:k]
		}
		// A copy: as a kept name, a slice would hold the whole source.
		return strings.Clone(rest)
	}
	return ""
}

// KeepModuleSource keeps a module's text, as compiled, under its name or URL for the line
// printed above a fatal exception from it. A load hook can replace a module's text, a
// module can have no file at all, and a file can change after it was compiled.
func (rt *Runtime) KeepModuleSource(name, source string) {
	rt.keepModule(source, "", name, sourceURL(source))
}

// KeepModuleSourceWithNUL is KeepModuleSource for text in which the character nul stands
// for the NUL characters of the module's source, which can't reach Go as they are. The
// header shows a line up to there, as Node shows it up to the NUL.
func (rt *Runtime) KeepModuleSourceWithNUL(name, source, nul string) {
	rt.keepModule(source, nul, name, sourceURL(source))
}

// ForgetModuleSource removes what KeepModuleSource kept for a module that then failed to
// compile.
func (rt *Runtime) ForgetModuleSource(name, source string) {
	rt.forgetModule(source, name, sourceURL(source))
}

// SetFirstLineOffset records, for a module kept under name, that its first line was
// compiled after offset characters of wrapper code, so stack columns on that line count
// them. The line printed above a fatal exception there puts its caret back under the
// source. Call it after the module's text is kept.
func (rt *Runtime) SetFirstLineOffset(name string, offset int) {
	if kept, ok := rt.keptSources[name]; ok && !kept.ambiguous {
		kept.offset = offset
	}
}
