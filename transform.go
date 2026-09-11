package phpcloak

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

var laravelHelpers = map[string]struct{}{
	"view": {}, "route": {}, "redirect": {}, "response": {}, "request": {}, "session": {}, "config": {},
	"app": {}, "auth": {}, "cache": {}, "collect": {}, "asset": {}, "url": {}, "old": {}, "now": {},
	"today": {}, "trans": {}, "__": {}, "abort": {}, "abort_if": {}, "abort_unless": {}, "back": {},
	"bcrypt": {}, "cookie": {}, "csrf_field": {}, "csrf_token": {}, "decrypt": {}, "dispatch": {}, "encrypt": {},
	"event": {}, "logger": {}, "optional": {}, "report": {}, "rescue": {}, "tap": {}, "validator": {}, "with": {},
	"data_get": {}, "data_set": {}, "filled": {}, "blank": {}, "value": {}, "retry": {}, "throw_if": {}, "throw_unless": {},
}

var protectedVariables = map[string]struct{}{
	"$this": {}, "$GLOBALS": {}, "$_SERVER": {}, "$_GET": {}, "$_POST": {}, "$_FILES": {},
	"$_COOKIE": {}, "$_SESSION": {}, "$_REQUEST": {}, "$_ENV": {},
}

var renameRiskNames = map[string]struct{}{
	"eval": {}, "include": {}, "include_once": {}, "require": {}, "require_once": {},
	"compact": {}, "extract": {}, "get_defined_vars": {}, "parse_str": {},
}

func Minify(source []byte) ([]byte, error) {
	tokens, err := lexPHP(source)
	if err != nil {
		return nil, err
	}
	out := renderTokens(tokens, nil)
	if err := Validate(out); err != nil {
		return nil, fmt.Errorf("native validation after minify: %w", err)
	}
	return out, nil
}

// Transform applies a pure-Go source transformation. Sealed mode is project-aware
// because its loader needs the target path and runtime key; use Protect for sealed mode.
func Transform(source []byte, mode Mode) (TransformResult, error) {
	if mode == ModeSealed {
		return TransformResult{}, fmt.Errorf("sealed mode requires project context; use Protect")
	}
	if !mode.Valid() {
		return TransformResult{}, fmt.Errorf("invalid mode %q", mode)
	}

	tokens, err := lexPHP(source)
	if err != nil {
		return TransformResult{}, err
	}
	if err := validateTokens(tokens); err != nil {
		return TransformResult{}, err
	}
	if mode == ModeSafe {
		out := renderTokens(tokens, nil)
		if err := Validate(out); err != nil {
			return TransformResult{}, fmt.Errorf("native validation after transform: %w", err)
		}
		return TransformResult{Source: out}, nil
	}

	protected := collectProtectedVariables(tokens)
	disableRename := renameMustBeDisabled(source, tokens)
	replacements := make(map[int]string)
	stats := TransformStats{RenameDisabled: disableRename}

	variableMap := make(map[string]string)
	counter := 0
	for i, tok := range tokens {
		if tok.kind != tokenVariable || disableRename {
			continue
		}
		if _, ok := protected[tok.text]; ok {
			continue
		}
		name, ok := variableMap[tok.text]
		if !ok {
			counter++
			sum := sha256.Sum256([]byte(tok.text + ":" + strconv.Itoa(counter) + ":" + strconv.Itoa(len(source))))
			name = "$_0x" + fmt.Sprintf("%x", sum[:6])
			variableMap[tok.text] = name
			stats.VariablesRenamed++
		}
		replacements[i] = name
	}

	if mode == ModeAggressive {
		for i, tok := range tokens {
			switch tok.kind {
			case tokenString:
				if value, ok := decodeStaticPHPString(tok.text); ok {
					replacements[i] = hideString(value)
					stats.StringsHidden++
				}
			case tokenIdentifier:
				next := nextSignificant(tokens, i)
				if next < 0 || tokens[next].text != "(" {
					continue
				}
				prev := prevSignificant(tokens, i)
				if prev >= 0 && (tokens[prev].text == "->" || tokens[prev].text == "?->") {
					replacements[i] = "{" + hideString(tok.text) + "}"
					stats.MethodsHidden++
					continue
				}
				if _, ok := laravelHelpers[strings.ToLower(tok.text)]; !ok || helperCallBlocked(tokens, prev) {
					continue
				}
				replacements[i] = "(" + hideString(tok.text) + ")"
				stats.HelpersHidden++
			}
		}
	}

	out := renderTokens(tokens, replacements)
	if err := Validate(out); err != nil {
		return TransformResult{}, fmt.Errorf("native validation after transform: %w", err)
	}
	return TransformResult{Source: out, Stats: stats}, nil
}

func validateTokens(tokens []phpToken) error {
	var stack []byte
	for _, tok := range tokens {
		if tok.kind == tokenRaw || tok.kind == tokenWhitespace || tok.kind == tokenComment || tok.kind == tokenString || tok.kind == tokenHeredoc {
			continue
		}
		if tok.kind == tokenOperator && tok.text == "#[" {
			stack = append(stack, '[')
			continue
		}
		if len(tok.text) != 1 {
			continue
		}
		switch tok.text[0] {
		case '(', '[', '{':
			stack = append(stack, tok.text[0])
		case ')', ']', '}':
			if len(stack) == 0 || !matchingDelimiter(stack[len(stack)-1], tok.text[0]) {
				return fmt.Errorf("invalid delimiter near byte %d", tok.pos)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("unclosed delimiter %q", string(stack[len(stack)-1]))
	}
	return nil
}

func collectProtectedVariables(tokens []phpToken) map[string]struct{} {
	out := make(map[string]struct{}, len(protectedVariables)+16)
	for name := range protectedVariables {
		out[name] = struct{}{}
	}

	// Function/closure/arrow-function parameter names are part of PHP's public
	// calling contract because named arguments and reflection can reference them.
	for i, tok := range tokens {
		if tok.kind != tokenIdentifier {
			continue
		}
		kw := strings.ToLower(tok.text)
		if kw != "function" && kw != "fn" {
			continue
		}
		open := findNextText(tokens, i, "(", "{", ";", "=>")
		if open < 0 || tokens[open].text != "(" {
			continue
		}
		close := matchingIndex(tokens, open, "(", ")")
		if close < 0 {
			continue
		}
		for j := open + 1; j < close; j++ {
			if tokens[j].kind == tokenVariable {
				out[tokens[j].text] = struct{}{}
			}
		}
	}

	// Closure capture variables: use ($x, &$y).
	for i, tok := range tokens {
		if tok.kind != tokenIdentifier || !strings.EqualFold(tok.text, "use") {
			continue
		}
		next := nextSignificant(tokens, i)
		if next < 0 || tokens[next].text != "(" {
			continue
		}
		close := matchingIndex(tokens, next, "(", ")")
		if close < 0 {
			continue
		}
		for j := next + 1; j < close; j++ {
			if tokens[j].kind == tokenVariable {
				out[tokens[j].text] = struct{}{}
			}
		}
	}

	// global $x, $y keeps linkage with the global symbol table.
	for i, tok := range tokens {
		if tok.kind != tokenIdentifier || !strings.EqualFold(tok.text, "global") {
			continue
		}
		for j := i + 1; j < len(tokens) && tokens[j].text != ";"; j++ {
			if tokens[j].kind == tokenVariable {
				out[tokens[j].text] = struct{}{}
			}
		}
	}

	// Protect class properties. Renaming properties would break external access,
	// serialization, reflection, Eloquent attributes and framework conventions.
	braceDepth := 0
	var classDepths []int
	var functionDepths []int
	pendingClass := false
	pendingFunction := false
	for i, tok := range tokens {
		if tok.kind == tokenWhitespace || tok.kind == tokenComment || tok.kind == tokenRaw || tok.kind == tokenOpenTag || tok.kind == tokenCloseTag {
			continue
		}
		if tok.kind == tokenIdentifier {
			kw := strings.ToLower(tok.text)
			prev := prevSignificant(tokens, i)
			if (kw == "class" || kw == "trait" || kw == "interface" || kw == "enum") && !(prev >= 0 && tokens[prev].text == "::") {
				pendingClass = true
			}
			if kw == "function" {
				pendingFunction = true
			}
		}
		if tok.kind == tokenVariable && len(classDepths) > 0 {
			classDepth := classDepths[len(classDepths)-1]
			insideFunction := len(functionDepths) > 0 && functionDepths[len(functionDepths)-1] >= classDepth
			if !insideFunction && braceDepth == classDepth {
				out[tok.text] = struct{}{}
			}
		}
		switch tok.text {
		case "{":
			braceDepth++
			if pendingClass {
				classDepths = append(classDepths, braceDepth)
				pendingClass = false
			} else if pendingFunction {
				functionDepths = append(functionDepths, braceDepth)
				pendingFunction = false
			}
		case "}":
			if len(functionDepths) > 0 && functionDepths[len(functionDepths)-1] == braceDepth {
				functionDepths = functionDepths[:len(functionDepths)-1]
			}
			if len(classDepths) > 0 && classDepths[len(classDepths)-1] == braceDepth {
				classDepths = classDepths[:len(classDepths)-1]
			}
			if braceDepth > 0 {
				braceDepth--
			}
		case ";":
			if pendingFunction {
				pendingFunction = false
			}
		}
	}
	return out
}

func renameMustBeDisabled(source []byte, tokens []phpToken) bool {
	s := string(source)
	if strings.Contains(s, "$$") || strings.Contains(s, "${") {
		return true
	}
	for _, tok := range tokens {
		if tok.kind == tokenIdentifier {
			if _, ok := renameRiskNames[strings.ToLower(tok.text)]; ok {
				return true
			}
		}
		if (tok.kind == tokenString || tok.kind == tokenHeredoc) && containsInterpolation(tok.text) {
			return true
		}
	}
	return false
}

func containsInterpolation(s string) bool {
	if len(s) == 0 || s[0] == '\'' {
		return false
	}
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == '$' {
			return true
		}
	}
	return false
}

func helperCallBlocked(tokens []phpToken, prev int) bool {
	if prev < 0 {
		return false
	}
	if tokens[prev].text == "\\" || tokens[prev].text == "::" {
		return true
	}
	if tokens[prev].kind != tokenIdentifier {
		return false
	}
	switch strings.ToLower(tokens[prev].text) {
	case "function", "new", "use", "namespace":
		return true
	default:
		return false
	}
}

func renderTokens(tokens []phpToken, replacements map[int]string) []byte {
	var b strings.Builder
	var prev *phpToken
	var prevText string
	for i, tok := range tokens {
		if tok.kind == tokenWhitespace || tok.kind == tokenComment {
			continue
		}
		text := tok.text
		if replacements != nil {
			if v, ok := replacements[i]; ok {
				text = v
			}
		}
		if tok.kind == tokenRaw {
			b.WriteString(text)
			prev = nil
			prevText = ""
			continue
		}
		if prev != nil && needsSeparator(*prev, prevText, tok, text) {
			b.WriteByte(' ')
		}
		b.WriteString(text)
		copyTok := tok
		if text != tok.text && (strings.HasPrefix(text, "(") || strings.HasPrefix(text, "{")) {
			copyTok.kind = tokenPunctuation
		}
		prev = &copyTok
		prevText = text
	}
	return []byte(b.String())
}

func needsSeparator(prev phpToken, prevText string, next phpToken, nextText string) bool {
	if prev.kind == tokenOpenTag {
		return next.kind != tokenCloseTag && next.kind != tokenRaw
	}
	if next.kind == tokenCloseTag {
		return false
	}
	if wordish(prev.kind) && wordish(next.kind) {
		return true
	}
	if prev.kind == tokenNumber && strings.HasPrefix(nextText, ".") {
		return true
	}
	if next.kind == tokenNumber && strings.HasSuffix(prevText, ".") {
		return true
	}
	joined := prevText + nextText
	for _, op := range multiOperators {
		if joined == op {
			return true
		}
	}
	switch joined {
	case "//", "/*", "?>", "<?":
		return true
	}
	return false
}

func wordish(kind tokenKind) bool {
	return kind == tokenIdentifier || kind == tokenVariable || kind == tokenNumber
}

func prevSignificant(tokens []phpToken, i int) int {
	for j := i - 1; j >= 0; j-- {
		if tokens[j].kind == tokenWhitespace || tokens[j].kind == tokenComment {
			continue
		}
		return j
	}
	return -1
}

func nextSignificant(tokens []phpToken, i int) int {
	for j := i + 1; j < len(tokens); j++ {
		if tokens[j].kind == tokenWhitespace || tokens[j].kind == tokenComment {
			continue
		}
		return j
	}
	return -1
}

func findNextText(tokens []phpToken, start int, wanted string, stops ...string) int {
	stop := make(map[string]struct{}, len(stops))
	for _, s := range stops {
		stop[s] = struct{}{}
	}
	for i := start + 1; i < len(tokens); i++ {
		if tokens[i].kind == tokenWhitespace || tokens[i].kind == tokenComment {
			continue
		}
		if tokens[i].text == wanted {
			return i
		}
		if _, ok := stop[tokens[i].text]; ok {
			return i
		}
	}
	return -1
}

func matchingIndex(tokens []phpToken, open int, openText, closeText string) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch tokens[i].text {
		case openText:
			depth++
		case closeText:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func decodeStaticPHPString(text string) (string, bool) {
	if len(text) < 2 {
		return "", false
	}
	quote := text[0]
	if text[len(text)-1] != quote || (quote != '\'' && quote != '"') {
		return "", false
	}
	body := text[1 : len(text)-1]
	if quote == '\'' {
		var b strings.Builder
		for i := 0; i < len(body); i++ {
			if body[i] == '\\' && i+1 < len(body) && (body[i+1] == '\\' || body[i+1] == '\'') {
				i++
				b.WriteByte(body[i])
				continue
			}
			b.WriteByte(body[i])
		}
		return b.String(), true
	}
	if containsInterpolation(text) {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			b.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'f':
			b.WriteByte('\f')
		case 'e':
			b.WriteByte(0x1b)
		case '\\', '"', '$':
			b.WriteByte(body[i])
		case 'x', 'X':
			if i+2 < len(body) && isHex(body[i+1]) && isHex(body[i+2]) {
				v, _ := strconv.ParseUint(body[i+1:i+3], 16, 8)
				b.WriteByte(byte(v))
				i += 2
			} else {
				b.WriteByte('\\')
				b.WriteByte(body[i])
			}
		default:
			if body[i] >= '0' && body[i] <= '7' {
				start := i
				end := i + 1
				for end < len(body) && end < start+3 && body[end] >= '0' && body[end] <= '7' {
					end++
				}
				v, _ := strconv.ParseUint(body[start:end], 8, 8)
				b.WriteByte(byte(v))
				i = end - 1
			} else {
				b.WriteByte('\\')
				b.WriteByte(body[i])
			}
		}
	}
	return b.String(), true
}

func hideString(s string) string {
	if s == "" {
		return `""`
	}
	mask := make([]byte, 0, len(s))
	for round := uint32(0); len(mask) < len(s); round++ {
		var counter [4]byte
		binary.BigEndian.PutUint32(counter[:], round)
		h := sha256.New()
		_, _ = h.Write([]byte("phpcloak-v3\x00"))
		_, _ = h.Write([]byte(s))
		_, _ = h.Write(counter[:])
		mask = append(mask, h.Sum(nil)...)
	}
	mask = mask[:len(s)]
	cipher := make([]byte, len(s))
	for i := range cipher {
		cipher[i] = s[i] ^ mask[i]
	}
	return "(" + rawHexString(mask) + "^" + rawHexString(cipher) + ")"
}

func rawHexString(b []byte) string {
	if len(b) == 0 {
		return `""`
	}
	var out strings.Builder
	out.Grow(2 + len(b)*4)
	out.WriteByte('"')
	for _, c := range b {
		fmt.Fprintf(&out, `\x%02X`, c)
	}
	out.WriteByte('"')
	return out.String()
}
