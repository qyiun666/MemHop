// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

package dsl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// TokenKind identifies the type of a lexer token.
type TokenKind int

const (
	TokEOF TokenKind = iota
	TokKeyword
	TokIdent
	TokString
	TokNumber
	TokLParen
	TokRParen
	TokLBracket
	TokRBracket
	TokColon
	TokDot
	TokComma
	TokStar
	TokOp
	TokMinus
)

// Token is a single lexer token.
type Token struct {
	Kind  TokenKind
	Value string
	Pos   int
}

// keywords recognized by the DSL.
var keywords = map[string]bool{
	"MATCH": true, "HYPEREDGE": true, "WHERE": true, "LIMIT": true,
	"PATH": true, "SUBGRAPH": true, "FROM": true, "DEPTH": true,
	"EDGE_KINDS": true, "AND": true, "OR": true, "CONTAINS": true,
}

// Parse parses a DSL query string into an AST.
func Parse(input string) (*Query, error) {
	tokens, err := Tokenize(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens, pos: 0}
	return p.parseQuery()
}

// Tokenize performs lexical analysis on the input string.
func Tokenize(input string) ([]Token, error) {
	var tokens []Token
	i := 0
	for i < len(input) {
		ch := input[i]
		if unicode.IsSpace(rune(ch)) {
			i++
			continue
		}
		switch {
		case ch == '"':
			tok, end, err := readString(input, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			i = end
		case ch == '(':
			tokens = append(tokens, Token{Kind: TokLParen, Value: "(", Pos: i})
			i++
		case ch == ')':
			tokens = append(tokens, Token{Kind: TokRParen, Value: ")", Pos: i})
			i++
		case ch == '[':
			tokens = append(tokens, Token{Kind: TokLBracket, Value: "[", Pos: i})
			i++
		case ch == ']':
			tokens = append(tokens, Token{Kind: TokRBracket, Value: "]", Pos: i})
			i++
		case ch == ':':
			tokens = append(tokens, Token{Kind: TokColon, Value: ":", Pos: i})
			i++
		case ch == '.':
			tokens = append(tokens, Token{Kind: TokDot, Value: ".", Pos: i})
			i++
		case ch == ',':
			tokens = append(tokens, Token{Kind: TokComma, Value: ",", Pos: i})
			i++
		case ch == '*':
			tokens = append(tokens, Token{Kind: TokStar, Value: "*", Pos: i})
			i++
		case ch == '-':
			tokens = append(tokens, Token{Kind: TokMinus, Value: "-", Pos: i})
			i++
		case ch == '!' && i+1 < len(input) && input[i+1] == '=':
			tokens = append(tokens, Token{Kind: TokOp, Value: "!=", Pos: i})
			i += 2
		case ch == '>' && i+1 < len(input) && input[i+1] == '=':
			tokens = append(tokens, Token{Kind: TokOp, Value: ">=", Pos: i})
			i += 2
		case ch == '<' && i+1 < len(input) && input[i+1] == '=':
			tokens = append(tokens, Token{Kind: TokOp, Value: "<=", Pos: i})
			i += 2
		case ch == '=':
			tokens = append(tokens, Token{Kind: TokOp, Value: "=", Pos: i})
			i++
		case ch == '>':
			tokens = append(tokens, Token{Kind: TokOp, Value: ">", Pos: i})
			i++
		case ch == '<':
			tokens = append(tokens, Token{Kind: TokOp, Value: "<", Pos: i})
			i++
		case unicode.IsDigit(rune(ch)):
			tok, end := readNumber(input, i)
			tokens = append(tokens, tok)
			i = end
		case unicode.IsLetter(rune(ch)) || ch == '_':
			tok, end := readIdentOrKeyword(input, i)
			tokens = append(tokens, tok)
			i = end
		default:
			return nil, fmt.Errorf("unexpected character %q at position %d", ch, i)
		}
	}
	tokens = append(tokens, Token{Kind: TokEOF, Value: "", Pos: i})
	return tokens, nil
}

func readString(input string, start int) (Token, int, error) {
	i := start + 1
	var sb strings.Builder
	for i < len(input) && input[i] != '"' {
		if input[i] == '\\' && i+1 < len(input) {
			i++
		}
		sb.WriteByte(input[i])
		i++
	}
	if i >= len(input) {
		return Token{}, 0, fmt.Errorf("unterminated string at position %d", start)
	}
	return Token{Kind: TokString, Value: sb.String(), Pos: start}, i + 1, nil
}

func readNumber(input string, start int) (Token, int) {
	i := start
	for i < len(input) && (unicode.IsDigit(rune(input[i])) || input[i] == '.') {
		i++
	}
	return Token{Kind: TokNumber, Value: input[start:i], Pos: start}, i
}

func readIdentOrKeyword(input string, start int) (Token, int) {
	i := start
	for i < len(input) && (unicode.IsLetter(rune(input[i])) || unicode.IsDigit(rune(input[i])) || input[i] == '_') {
		i++
	}
	val := input[start:i]
	kind := TokIdent
	if keywords[val] {
		kind = TokKeyword
	}
	return Token{Kind: kind, Value: val, Pos: start}, i
}

// parser is a recursive descent parser for the DSL.
type parser struct {
	tokens []Token
	pos    int
}

func (p *parser) peek() Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return Token{Kind: TokEOF}
}

func (p *parser) advance() Token {
	t := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *parser) expect(kind TokenKind, value string) (Token, error) {
	t := p.advance()
	if t.Kind != kind {
		return t, fmt.Errorf("expected %s %q at pos %d, got %q", tokenKindName(kind), value, t.Pos, t.Value)
	}
	if value != "" && t.Value != value {
		return t, fmt.Errorf("expected %q at pos %d, got %q", value, t.Pos, t.Value)
	}
	return t, nil
}

func (p *parser) expectKeyword(kw string) error {
	_, err := p.expect(TokKeyword, kw)
	return err
}

func (p *parser) isKeyword(kw string) bool {
	t := p.peek()
	return t.Kind == TokKeyword && t.Value == kw
}

// parseQuery dispatches to the appropriate clause parser.
func (p *parser) parseQuery() (*Query, error) {
	t := p.peek()
	if t.Kind != TokKeyword {
		return nil, fmt.Errorf("expected keyword at pos %d, got %q", t.Pos, t.Value)
	}
	switch t.Value {
	case "MATCH":
		return p.parseMatchOrHyperedge()
	case "PATH":
		return p.parsePath()
	case "SUBGRAPH":
		return p.parseSubgraph()
	default:
		return nil, fmt.Errorf("unknown query type %q at pos %d", t.Value, t.Pos)
	}
}

// parseMatchOrHyperedge parses MATCH (n:type) or MATCH HYPEREDGE e-[...]-
func (p *parser) parseMatchOrHyperedge() (*Query, error) {
	p.advance() // consume MATCH
	if p.isKeyword("HYPEREDGE") {
		return p.parseHyperedge()
	}
	return p.parseMatch()
}

func (p *parser) parseMatch() (*Query, error) {
	if _, err := p.expect(TokLParen, "("); err != nil {
		return nil, err
	}
	m := &NodeMatch{}
	// optional variable
	if p.peek().Kind == TokIdent {
		m.Variable = p.advance().Value
	}
	// optional :type_label
	if p.peek().Kind == TokColon {
		p.advance()
		t, err := p.expect(TokIdent, "")
		if err != nil {
			return nil, fmt.Errorf("expected type label: %w", err)
		}
		m.NodeType = t.Value
	}
	if _, err := p.expect(TokRParen, ")"); err != nil {
		return nil, err
	}
	// optional WHERE
	if p.isKeyword("WHERE") {
		wc, err := p.parseWhere()
		if err != nil {
			return nil, err
		}
		m.WhereClause = wc
	}
	// optional LIMIT
	if p.isKeyword("LIMIT") {
		lim, err := p.parseLimit()
		if err != nil {
			return nil, err
		}
		m.Limit = lim
	}
	return &Query{Match: m}, nil
}

func (p *parser) parseHyperedge() (*Query, error) {
	p.advance() // consume HYPEREDGE
	edgeVar, err := p.expect(TokIdent, "")
	if err != nil {
		return nil, fmt.Errorf("expected edge variable: %w", err)
	}
	h := &HyperedgeMatch{EdgeVar: edgeVar.Value}
	// expect -[
	if _, err := p.expect(TokMinus, "-"); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokLBracket, "["); err != nil {
		return nil, err
	}
	// parse variable list (syntax validation only; the executor does
	// not restrict which nodes the hyperedge connects)
	if _, err := p.parseVarList(); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokRBracket, "]"); err != nil {
		return nil, err
	}
	if _, err := p.expect(TokMinus, "-"); err != nil {
		return nil, err
	}
	if p.isKeyword("WHERE") {
		wc, err := p.parseWhere()
		if err != nil {
			return nil, err
		}
		h.WhereClause = wc
	}
	if p.isKeyword("LIMIT") {
		lim, err := p.parseLimit()
		if err != nil {
			return nil, err
		}
		h.Limit = lim
	}
	return &Query{Hyperedge: h}, nil
}

func (p *parser) parseVarList() ([]string, error) {
	var vars []string
	t, err := p.expect(TokIdent, "")
	if err != nil {
		return nil, err
	}
	vars = append(vars, t.Value)
	for p.peek().Kind == TokComma {
		p.advance()
		t, err := p.expect(TokIdent, "")
		if err != nil {
			return nil, err
		}
		vars = append(vars, t.Value)
	}
	return vars, nil
}

func (p *parser) parsePath() (*Query, error) {
	p.advance() // consume PATH
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	startTok, err := p.expect(TokString, "")
	if err != nil {
		return nil, fmt.Errorf("expected start node string: %w", err)
	}
	if err := p.expectKeyword("DEPTH"); err != nil {
		return nil, err
	}
	depthTok, err := p.expect(TokNumber, "")
	if err != nil {
		return nil, fmt.Errorf("expected depth number: %w", err)
	}
	depth, err := strconv.Atoi(depthTok.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid depth %q: %w", depthTok.Value, err)
	}
	pq := &PathQuery{StartNode: startTok.Value, MaxDepth: depth}
	// optional EDGE_KINDS [...]
	if p.isKeyword("EDGE_KINDS") {
		p.advance()
		kinds, err := p.parseStringList()
		if err != nil {
			return nil, err
		}
		pq.EdgeKinds = kinds
	}
	return &Query{Path: pq}, nil
}

func (p *parser) parseSubgraph() (*Query, error) {
	p.advance() // consume SUBGRAPH
	if err := p.expectKeyword("FROM"); err != nil {
		return nil, err
	}
	startTok, err := p.expect(TokString, "")
	if err != nil {
		return nil, fmt.Errorf("expected start node string: %w", err)
	}
	if err := p.expectKeyword("DEPTH"); err != nil {
		return nil, err
	}
	depthTok, err := p.expect(TokNumber, "")
	if err != nil {
		return nil, fmt.Errorf("expected depth number: %w", err)
	}
	depth, err := strconv.Atoi(depthTok.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid depth %q: %w", depthTok.Value, err)
	}
	return &Query{Subgraph: &SubgraphQuery{StartNode: startTok.Value, MaxDepth: depth}}, nil
}

func (p *parser) parseStringList() ([]string, error) {
	if _, err := p.expect(TokLBracket, "["); err != nil {
		return nil, err
	}
	var items []string
	if p.peek().Kind == TokString {
		items = append(items, p.advance().Value)
		for p.peek().Kind == TokComma {
			p.advance()
			t, err := p.expect(TokString, "")
			if err != nil {
				return nil, err
			}
			items = append(items, t.Value)
		}
	}
	if _, err := p.expect(TokRBracket, "]"); err != nil {
		return nil, err
	}
	return items, nil
}

// parseWhere parses WHERE condition (or_condition with AND/OR).
func (p *parser) parseWhere() (*WhereCondition, error) {
	p.advance() // consume WHERE
	return p.parseOrCondition()
}

func (p *parser) parseOrCondition() (*WhereCondition, error) {
	left, err := p.parseAndCondition()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("OR") {
		p.advance()
		right, err := p.parseAndCondition()
		if err != nil {
			return nil, err
		}
		left = &WhereCondition{Or: &BinaryCondition{Left: left, Right: right}}
	}
	return left, nil
}

func (p *parser) parseAndCondition() (*WhereCondition, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("AND") {
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &WhereCondition{And: &BinaryCondition{Left: left, Right: right}}
	}
	return left, nil
}

func (p *parser) parsePrimary() (*WhereCondition, error) {
	t := p.peek()
	// parenthesized sub-condition
	if t.Kind == TokLParen {
		p.advance()
		cond, err := p.parseOrCondition()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokRParen, ")"); err != nil {
			return nil, err
		}
		return cond, nil
	}
	// all other primaries start with variable.property or variable.keywords CONTAINS
	return p.parseConditionAtom()
}

// parseConditionAtom parses property_compare | type_equals | keyword_contains.
func (p *parser) parseConditionAtom() (*WhereCondition, error) {
	varTok, err := p.expect(TokIdent, "")
	if err != nil {
		return nil, fmt.Errorf("expected variable name: %w", err)
	}
	_ = varTok // variable name prefix (e.g. "n")
	if _, err := p.expect(TokDot, "."); err != nil {
		return nil, err
	}
	propTok := p.advance()
	if propTok.Kind != TokIdent && propTok.Kind != TokKeyword {
		return nil, fmt.Errorf("expected property name at pos %d, got %q", propTok.Pos, propTok.Value)
	}
	prop := propTok.Value
	// keyword_contains: n.keywords CONTAINS "value"
	if prop == "keywords" && p.isKeyword("CONTAINS") {
		p.advance()
		val, err := p.expect(TokString, "")
		if err != nil {
			return nil, fmt.Errorf("expected string after CONTAINS: %w", err)
		}
		s := val.Value
		return &WhereCondition{KeywordContains: &s}, nil
	}
	// type_equals: n.type = "value"
	if prop == "type" {
		if _, err := p.expect(TokOp, "="); err != nil {
			return nil, err
		}
		val, err := p.expect(TokString, "")
		if err != nil {
			return nil, fmt.Errorf("expected string after =: %w", err)
		}
		s := val.Value
		return &WhereCondition{TypeEquals: &s}, nil
	}
	// property_compare: n.importance > 0.5
	opTok, err := p.expect(TokOp, "")
	if err != nil {
		return nil, fmt.Errorf("expected comparison operator: %w", err)
	}
	numTok, err := p.expect(TokNumber, "")
	if err != nil {
		return nil, fmt.Errorf("expected number value: %w", err)
	}
	numVal, err := strconv.ParseFloat(numTok.Value, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid number %q: %w", numTok.Value, err)
	}
	op, err := parseCompareOp(opTok.Value)
	if err != nil {
		return nil, err
	}
	return &WhereCondition{
		PropertyCompare: &PropertyCompareCondition{
			Property: prop, Operator: op, Value: float32(numVal),
		},
	}, nil
}

func parseCompareOp(s string) (CompareOp, error) {
	switch s {
	case ">":
		return OpGt, nil
	case ">=":
		return OpGe, nil
	case "<":
		return OpLt, nil
	case "<=":
		return OpLe, nil
	case "=":
		return OpEq, nil
	case "!=":
		return OpNe, nil
	default:
		return 0, fmt.Errorf("unknown operator %q", s)
	}
}

func (p *parser) parseLimit() (int, error) {
	p.advance() // consume LIMIT
	t, err := p.expect(TokNumber, "")
	if err != nil {
		return 0, fmt.Errorf("expected limit number: %w", err)
	}
	n, err := strconv.Atoi(t.Value)
	if err != nil {
		return 0, fmt.Errorf("invalid limit %q: %w", t.Value, err)
	}
	return n, nil
}

func tokenKindName(k TokenKind) string {
	switch k {
	case TokEOF:
		return "EOF"
	case TokKeyword:
		return "keyword"
	case TokIdent:
		return "identifier"
	case TokString:
		return "string"
	case TokNumber:
		return "number"
	case TokLParen:
		return "("
	case TokRParen:
		return ")"
	case TokLBracket:
		return "["
	case TokRBracket:
		return "]"
	case TokOp:
		return "operator"
	default:
		return "token"
	}
}
