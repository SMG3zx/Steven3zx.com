package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// SExpression
type SExpression interface {
	String() string
	// Eval computes the value of the expression using env to look up variables.
	Eval(env map[string]float64) (float64, error)
	// Diff returns the derivative of the expression with respect to the variable name.
	Diff(name string) SExpression
}

// Token is a sealed interface representing the enum
type Token interface {
	isToken()
}

// Atom represents Token::Atom(String), a number or a variable name
type Atom struct {
	Value string
}

func (Atom) isToken() {}

func (a Atom) String() string {
	return a.Value
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

func NewLexer(input string) *Lexer {
	var tokens []Token
	runes := []rune(input)

	for i := 0; i < len(runes); {
		c := runes[i]

		switch {
		case unicode.IsSpace(c):
			i++

		// A number is a run of digits and dots, such as 12 or 3.5
		case unicode.IsDigit(c):
			start := i
			for i < len(runes) && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
				i++
			}
			tokens = append(tokens, Atom{Value: string(runes[start:i])})

		// A name is a letter followed by letters or digits, such as x or x2
		case unicode.IsLetter(c):
			start := i
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
				i++
			}
			tokens = append(tokens, Atom{Value: string(runes[start:i])})

		default:
			tokens = append(tokens, Op{Value: c})
			i++
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
	Rest []SExpression
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

// Eval evaluates both operands and applies the operator. Division by zero is an error.
func (c Cons) Eval(env map[string]float64) (float64, error) {
	left, err := c.Rest[0].Eval(env)
	if err != nil {
		return 0, err
	}
	right, err := c.Rest[1].Eval(env)
	if err != nil {
		return 0, err
	}

	switch c.Head {
	case '+':
		return left + right, nil
	case '-':
		return left - right, nil
	case '*':
		return left * right, nil
	case '/':
		if right == 0 {
			return 0, fmt.Errorf("division by zero in %s", c)
		}
		return left / right, nil
	case '^':
		return math.Pow(left, right), nil
	}
	return 0, fmt.Errorf("unknown operator %c", c.Head)
}

// Diff applies the sum, product, quotient, and power rules to the operands.
// The power rule only handles exponents that contain no variables, so
// anything else panics, like the other syntax errors in this file.
func (c Cons) Diff(name string) SExpression {
	a, b := c.Rest[0], c.Rest[1]
	da, db := a.Diff(name), b.Diff(name)

	switch c.Head {
	case '+':
		return cons('+', da, db)
	case '-':
		return cons('-', da, db)
	case '*':
		// (a * b)' = a' * b + a * b'
		return cons('+', cons('*', da, b), cons('*', a, db))
	case '/':
		// (a / b)' = (a' * b - a * b') / b^2
		return cons('/', cons('-', cons('*', da, b), cons('*', a, db)), cons('^', b, number(2)))
	case '^':
		if _, err := b.Eval(nil); err != nil {
			panic(fmt.Sprintf("cannot differentiate %s: exponent is not a constant", c))
		}
		// (a ^ b)' = b * a ^ (b - 1) * a'
		return cons('*', cons('*', b, cons('^', a, cons('-', b, number(1)))), da)
	}
	panic(fmt.Sprintf("cannot differentiate operator %c", c.Head))
}

func cons(head rune, rest ...SExpression) Cons {
	return Cons{Head: head, Rest: rest}
}

// SAtom represents S::Atom(String), a number or a variable name
type SAtom struct {
	Value string
}

func (a SAtom) String() string {
	return a.Value
}

// Eval looks the name up in env first. Otherwise a leading digit means a number.
func (a SAtom) Eval(env map[string]float64) (float64, error) {
	if v, ok := env[a.Value]; ok {
		return v, nil
	}
	if unicode.IsDigit(rune(a.Value[0])) {
		return strconv.ParseFloat(a.Value, 64)
	}
	return 0, fmt.Errorf("unknown variable %q", a.Value)
}

// Diff of a variable is 1 for itself and 0 for everything else.
func (a SAtom) Diff(name string) SExpression {
	if a.Value == name {
		return number(1)
	}
	return number(0)
}

// number builds a numeric atom for a constant produced by Diff.
func number(f float64) SAtom {
	return SAtom{Value: strconv.FormatFloat(f, 'f', -1, 64)}
}

// expr parses a whole input string into an S tree.
// It starts with minBindingPower 0, so every operator is allowed to join the expression.
func expr(input string) SExpression {
	lexer := NewLexer(input)
	tree := exprBindingPower(lexer, 0)

	// Anything left over, such as an unmatched ")", is a syntax error.
	if _, ok := lexer.Peek().(Eof); !ok {
		panic(fmt.Sprintf("unexpected token: %v", lexer.Peek()))
	}
	return tree
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
func exprBindingPower(lexer *Lexer, minBindingPower int) SExpression {
	// The left-hand side is a name or number, or a parenthesized expression.
	// Anything else is a syntax error, so we panic.
	var lhs SExpression
	switch t := lexer.Next().(type) {
	case Atom:
		lhs = SAtom{Value: t.Value}
	case Op:
		if t.Value != '(' {
			panic(fmt.Sprintf("bad token: %v", t))
		}
		lhs = exprBindingPower(lexer, 0)
		closing, ok := lexer.Next().(Op)
		if !ok || closing.Value != ')' {
			panic(fmt.Sprintf("expected ')', got %v", closing))
		}
	default:
		panic(fmt.Sprintf("bad token: %v", t))
	}

	// Each pass of the loop tries to attach one more operator to lhs(Left Hand Side).
	for {
		// Look at the next token without consuming it. Eof means the input is
		// done, so return what we have. A ")" closes a parenthesized group, so the
		// caller handles it. An operator is the only other valid token.
		var op rune
		switch t := lexer.Peek().(type) {
		case Eof:
			return lhs
		case Op:
			if t.Value == ')' {
				return lhs
			}
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
		lhs = Cons{Head: op, Rest: []SExpression{lhs, rhs}}
	}
}

// infixBindingPower returns the left and right binding power of an operator.
// Higher numbers bind more tightly. For left-associative operators the right power
// is one more than the left, so chains like "1 - 2 - 3" group to the left as ((1 - 2) - 3).
// For right-associative "^" the right power is one less, so "2 ^ 3 ^ 2" groups to the right.
func infixBindingPower(op rune) (int, int) {
	switch op {
	case '+', '-':
		return 1, 2
	case '*', '/':
		return 3, 4
	case '^':
		return 6, 5
	}
	panic(fmt.Sprintf("bad op: %c", op))
}

func main() {

}
