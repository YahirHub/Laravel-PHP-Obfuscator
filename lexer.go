package phpcloak

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

type tokenKind uint8

const (
	tokenRaw tokenKind = iota
	tokenOpenTag
	tokenCloseTag
	tokenWhitespace
	tokenComment
	tokenIdentifier
	tokenVariable
	tokenNumber
	tokenString
	tokenHeredoc
	tokenOperator
	tokenPunctuation
)

type phpToken struct {
	kind tokenKind
	text string
	pos  int
}

var multiOperators = []string{
	"?->", "??=", "===", "!==", "<=>", "<<=", ">>=", "**=", "...",
	"->", "::", "=>", "==", "!=", "<=", ">=", "&&", "||", "??", "++", "--",
	"+=", "-=", "*=", "/=", ".=", "%=", "&=", "|=", "^=", "<<", ">>", "**", "#[",
}

func lexPHP(src []byte) ([]phpToken, error) {
	if len(src) == 0 {
		return nil, errors.New("empty PHP source")
	}

	var out []phpToken
	inPHP := false
	seenTag := false

	for i := 0; i < len(src); {
		if !inPHP {
			j := bytes.Index(src[i:], []byte("<?"))
			if j < 0 {
				if !seenTag {
					return nil, errors.New("PHP opening tag not found")
				}
				if i < len(src) {
					out = append(out, phpToken{kind: tokenRaw, text: string(src[i:]), pos: i})
				}
				break
			}
			j += i
			if j > i {
				out = append(out, phpToken{kind: tokenRaw, text: string(src[i:j]), pos: i})
			}
			open := "<?"
			if bytes.HasPrefix(src[j:], []byte("<?=")) {
				open = "<?="
			} else if len(src)-j >= 5 && strings.EqualFold(string(src[j:j+5]), "<?php") {
				open = string(src[j : j+5])
			}
			out = append(out, phpToken{kind: tokenOpenTag, text: open, pos: j})
			i = j + len(open)
			inPHP = true
			seenTag = true
			continue
		}

		if bytes.HasPrefix(src[i:], []byte("?>")) {
			out = append(out, phpToken{kind: tokenCloseTag, text: "?>", pos: i})
			i += 2
			inPHP = false
			continue
		}

		c := src[i]
		if isSpace(c) {
			j := i + 1
			for j < len(src) && isSpace(src[j]) {
				j++
			}
			out = append(out, phpToken{kind: tokenWhitespace, text: string(src[i:j]), pos: i})
			i = j
			continue
		}

		if bytes.HasPrefix(src[i:], []byte("//")) {
			j := i + 2
			for j < len(src) && src[j] != '\n' && src[j] != '\r' {
				j++
			}
			out = append(out, phpToken{kind: tokenComment, text: string(src[i:j]), pos: i})
			i = j
			continue
		}
		if c == '#' && !bytes.HasPrefix(src[i:], []byte("#[")) {
			j := i + 1
			for j < len(src) && src[j] != '\n' && src[j] != '\r' {
				j++
			}
			out = append(out, phpToken{kind: tokenComment, text: string(src[i:j]), pos: i})
			i = j
			continue
		}
		if bytes.HasPrefix(src[i:], []byte("/*")) {
			j := bytes.Index(src[i+2:], []byte("*/"))
			if j < 0 {
				return nil, fmt.Errorf("unterminated block comment at byte %d", i)
			}
			j += i + 4
			out = append(out, phpToken{kind: tokenComment, text: string(src[i:j]), pos: i})
			i = j
			continue
		}

		if c == '\'' || c == '"' || c == '`' {
			j, err := scanQuoted(src, i, c)
			if err != nil {
				return nil, err
			}
			out = append(out, phpToken{kind: tokenString, text: string(src[i:j]), pos: i})
			i = j
			continue
		}

		if bytes.HasPrefix(src[i:], []byte("<<<")) {
			j, ok, err := scanHeredoc(src, i)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, phpToken{kind: tokenHeredoc, text: string(src[i:j]), pos: i})
				i = j
				continue
			}
		}

		if c == '$' && i+1 < len(src) && isIdentStart(src[i+1]) {
			j := i + 2
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			out = append(out, phpToken{kind: tokenVariable, text: string(src[i:j]), pos: i})
			i = j
			continue
		}

		if isIdentStart(c) {
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			out = append(out, phpToken{kind: tokenIdentifier, text: string(src[i:j]), pos: i})
			i = j
			continue
		}

		if isDigit(c) {
			j := scanNumber(src, i)
			out = append(out, phpToken{kind: tokenNumber, text: string(src[i:j]), pos: i})
			i = j
			continue
		}

		matched := ""
		for _, op := range multiOperators {
			if bytes.HasPrefix(src[i:], []byte(op)) {
				matched = op
				break
			}
		}
		if matched != "" {
			out = append(out, phpToken{kind: tokenOperator, text: matched, pos: i})
			i += len(matched)
			continue
		}

		out = append(out, phpToken{kind: tokenPunctuation, text: string(c), pos: i})
		i++
	}

	if inPHP {
		// PHP files do not require a closing tag; remaining source was consumed in PHP mode.
	}
	return out, nil
}

func scanQuoted(src []byte, start int, quote byte) (int, error) {
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] == quote {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated quoted string at byte %d", start)
}

func scanHeredoc(src []byte, start int) (end int, ok bool, err error) {
	lineEnd := bytes.IndexByte(src[start:], '\n')
	if lineEnd < 0 {
		return 0, false, nil
	}
	lineEnd += start
	header := strings.TrimSpace(string(src[start+3 : lineEnd]))
	if strings.HasSuffix(header, "\r") {
		header = strings.TrimSuffix(header, "\r")
	}
	if len(header) >= 2 && ((header[0] == '\'' && header[len(header)-1] == '\'') || (header[0] == '"' && header[len(header)-1] == '"')) {
		header = header[1 : len(header)-1]
	}
	if header == "" || !isIdentifierString(header) {
		return 0, false, nil
	}

	pos := lineEnd + 1
	for pos <= len(src) {
		next := bytes.IndexByte(src[pos:], '\n')
		var line []byte
		var after int
		if next < 0 {
			line = src[pos:]
			after = len(src)
		} else {
			next += pos
			line = src[pos:next]
			after = next + 1
		}
		trimmed := strings.TrimSpace(strings.TrimSuffix(string(line), "\r"))
		if trimmed == header || trimmed == header+";" {
			return after, true, nil
		}
		if next < 0 {
			break
		}
		pos = after
	}
	return 0, false, fmt.Errorf("unterminated heredoc %q at byte %d", header, start)
}

func scanNumber(src []byte, start int) int {
	i := start
	if i+2 <= len(src) && src[i] == '0' && i+1 < len(src) {
		switch src[i+1] {
		case 'x', 'X':
			i += 2
			for i < len(src) && (isHex(src[i]) || src[i] == '_') {
				i++
			}
			return i
		case 'b', 'B':
			i += 2
			for i < len(src) && (src[i] == '0' || src[i] == '1' || src[i] == '_') {
				i++
			}
			return i
		case 'o', 'O':
			i += 2
			for i < len(src) && ((src[i] >= '0' && src[i] <= '7') || src[i] == '_') {
				i++
			}
			return i
		}
	}
	seenDot := false
	seenExp := false
	for i < len(src) {
		c := src[i]
		switch {
		case isDigit(c) || c == '_':
			i++
		case c == '.' && !seenDot && !seenExp:
			seenDot = true
			i++
		case (c == 'e' || c == 'E') && !seenExp:
			seenExp = true
			i++
			if i < len(src) && (src[i] == '+' || src[i] == '-') {
				i++
			}
		default:
			return i
		}
	}
	return i
}

func Validate(source []byte) error {
	tokens, err := lexPHP(source)
	if err != nil {
		return err
	}
	if err := validateKnownTokenCollisions(tokens); err != nil {
		return err
	}
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
			if len(stack) == 0 {
				return fmt.Errorf("unexpected %q at byte %d", tok.text, tok.pos)
			}
			open := stack[len(stack)-1]
			if !matchingDelimiter(open, tok.text[0]) {
				return fmt.Errorf("mismatched delimiter %q for %q at byte %d", tok.text, string(open), tok.pos)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 {
		return fmt.Errorf("unclosed delimiter %q", string(stack[len(stack)-1]))
	}
	return nil
}

func validateKnownTokenCollisions(tokens []phpToken) error {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].kind != tokenIdentifier || !strings.EqualFold(tokens[i].text, "instanceof") {
			continue
		}
		if tokens[i+1].text == `\` {
			return fmt.Errorf("missing whitespace after instanceof before namespace separator at byte %d", tokens[i+1].pos)
		}
	}
	return nil
}

func matchingDelimiter(open, close byte) bool {
	return (open == '(' && close == ')') || (open == '[' && close == ']') || (open == '{' && close == '}')
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\v', '\f':
		return true
	default:
		return false
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

func isIdentifierString(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentPart(s[i]) {
			return false
		}
	}
	return true
}
