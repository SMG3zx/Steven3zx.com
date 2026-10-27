package main

import (
	"fmt"
	"strings"
	"unicode"
)

// Token is a sealed interface representing the enum
type Token interface {
	isToken()
}

// Atom represents Token::Atom(char)
type Atom struct {
	Value rune
}

func (Atom) isToken() {}

func (a Atom) String() string {
	return string(a.Value)
}

// Op represents Token::Op(char)
type Op struct {
	Value rune
}

func (Op) isToken() {}

// Eof represents Token::Eof (a zero-sized unit variant)
type Eof struct{}

func (Eof) isToken() {}

type Lexer struct {
	Tokens []Token
}

// S is the interface representing the sum type
type S interface {
	String() string
}

func NewLexer(input string) *Lexer {
	var tokens []Token

	// Filter out whitespace and map charecters to tokens
	for _, c := range input {
		if unicode.IsSpace(c) {
			continue
		}

		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			tokens = append(tokens, Atom{Value: c})
		} else {
			tokens = append(tokens, Op{Value: c})
		}
	}

	// Reverse the slice
	for i, j := 0, len(tokens)-1; i < j; i, j = i+1, j-1 {
		tokens[i], tokens[j] = tokens[j], tokens[i]
	}

	return &Lexer{Tokens: tokens}
}

func (l *Lexer) Next() Token {
	if len(l.Tokens) == 0 {
		return Eof{}
	}

	// Pop from the end of the slice
	index := len(l.Tokens) - 1
	token := l.Tokens[index]
	l.Tokens = l.Tokens[:index]

	return token
}

func (l *Lexer) Peek() Token {
	if len(l.Tokens) == 0 {
		return Eof{}
	}

	// Return the last element without removing it
	return l.Tokens[len(l.Tokens)-1]
}

type Cons struct {
	Head rune
	Rest []S
}

func (c Cons) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "(%c", c.Head)
	for _, s := range c.Rest {
		fmt.Fprintf(&sb, " %s", s.String())
	}
	sb.WriteString(")")
	return sb.String()
}

type SAtom struct {
	Value rune
}

func (a SAtom) String() string {
	return string(a.Value)
}

// expr parses a whole input string into an S tree.
// It starts with minBindingPower 0, so every operator is allowed to join the expression.
func expr(input string) S {
	lexer := NewLexer(input)
	return exprBindingPower(lexer, 0)
}

// exprBindingPower parses one expression from the lexer. minBindingPower is the lowest left binding
// power an operator may have and still be consumed here; anything weaker is left
// for a caller further up the recursion.
//
// For "1 + 2 * 3" the calls go like this:
//   - exprBindingPower(0) reads 1, sees +, and takes it (left power 1 >= 0).
//   - It recurses with minBindingPower 2 (the right power of +) to parse the right side.
//   - That call reads 2, sees *, and takes it (left power 3 >= 2).
//   - It recurses with minBindingPower 4 and reads 3. Then it sees Eof and returns 3.
//   - The * call builds (* 2 3) and returns it to the + call.
//   - The + call builds (+ 1 (* 2 3)) and returns it.
func exprBindingPower(lexer *Lexer, minBindingPower int) S {
	// The left-hand side must start with an atom (a number or name).
	// Anything else is a syntax error, so we panic.
	var lhs S
	switch t := lexer.Next().(type) {
	case Atom:
		lhs = SAtom{Value: t.Value}
	default:
		panic(fmt.Sprintf("bad token: %v", t))
	}

	// Each pass of the loop tries to attach one more operator to lhs(Left Hand Side).
	for {
		// Look at the next token without consuming it. Eof means the input is
		// done, so return what we have. An operator is the only other valid token.
		var op rune
		switch t := lexer.Peek().(type) {
		case Eof:
			return lhs
		case Op:
			op = t.Value
		default:
			panic(fmt.Sprintf("bad token: %v", t))
		}

		// If this operator binds less tightly than what the caller asked for,
		// stop here. The caller will use lhs as its own left-hand side.
		lBP, rBP := infixBindingPower(op)
		if lBP < minBindingPower {
			return lhs
		}
		// Consume the operator token now that we know we are using it.
		lexer.Next()

		// Parse the right-hand side with rBP as the minimum. Operators that bind
		// more tightly than op are absorbed into rhs. Weaker ones are left for the
		// loop here.
		rhs := exprBindingPower(lexer, rBP)
		lhs = Cons{Head: op, Rest: []S{lhs, rhs}}
	}
}

// infixBindingPower returns the left and right binding power of an operator.
// Higher numbers bind more tightly. The right power is always one more than the
// left, so chains like "1 - 2 - 3" group to the left as ((1 - 2) - 3).
func infixBindingPower(op rune) (int, int) {
	switch op {
	case '+', '-':
		return 1, 2
	case '*', '/':
		return 3, 4
	}
	panic(fmt.Sprintf("bad op: %c", op))
}

func main() {

}
