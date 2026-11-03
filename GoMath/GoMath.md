https://medium.com/better-programming/building-a-computer-algebra-system-in-go-part-1-multivariate-expressions-and-differentiation-291d948aa0e4

# GoMath

GoMath parses algebra written in ASCII or TeX, shows each parsing and algebra step, and checks its answers against the worksheets in `equations/`.

Run the TUI with `go run .` in this directory. Tests run with `go test ./...`.

## Input

### ASCII

| Element | Examples | Notes |
| --- | --- | --- |
| Whole numbers | `12`, `-4` | Polynomial operations need whole-number coefficients. |
| Decimal numbers | `3.5` | Accepted by `Eval` and `Diff`. Rejected by polynomial operations. |
| Variables | `x`, `y` | One letter each. `xy` means `x*y`. `x2` means `x*2`. |
| Operators | `+ - * / ^` | Binary `+ - * /` and power `^`. |
| Implicit multiplication | `2x`, `7x^2y`, `2(x + 1)`, `(x+4)(x-4)` | Same binding as `*`. |
| Unary minus | `-x^2`, `-4x` | `-x^2` is `-(x^2)`. |
| Grouping | `(x + 1)(x - 1)` | Parentheses may nest. |
| Spaces | `x + 1` | Ignored. |

Precedence, tightest first:

1. Parentheses.
2. `^`, right-associative: `2^3^2` is `2^(3^2)`.
3. Unary minus.
4. `*`, `/`, and implicit multiplication, left-associative.
5. Binary `+` and `-`, left-associative.

### TeX

TeX is rewritten to ASCII before parsing. The rewrites are listed in the TUI's first steps.

| TeX | ASCII |
| --- | --- |
| `\left(` and `\right)` | `(` and `)` |
| `\cdot`, `\times` | `*` |
| `\frac{a}{b}` | `(a)/(b)` |
| `{...}`, as in `x^{2}` | `(...)`, giving `x^(2)` |
| `\,` | a space |
| Unicode minus `−` | `-` |

Other TeX commands such as `\sqrt`, `\sin`, `\pi`, subscripts, and `\begin{...}` are not supported.

## Operations

| Operation | TUI key | What it does | Example |
| --- | --- | --- | --- |
| Simplify | enter | Combines like terms and writes the standard form with its degree. | `(x+4)(x^2-4x+16)` gives `x^3 + 64`, degree 3. |
| Factor | ctrl+f | Pulls out the integer content and common variable powers, then finds rational roots. | `16x + 40` gives `8(2x + 5)`. |
| Technique | ctrl+t | Names the factoring method that fits the shape. | `64x^3 - 125` gives `difference of cubes`. |
| Prime check | ctrl+p | Reports whether the polynomial is irreducible over the integers. | `x^2 - 2x - 2` gives `true`. |
| Root blank | ctrl+r | Fills the blank in "sqrt(n) = r because ___ = n" with `r^2`. `n` must be a perfect square. Accepts `sqrt(169)` or `\sqrt{169}`. | `sqrt(169)` gives `13^2`. |

The library also has `Eval` (a number from variable values) and `Diff` (a derivative with respect to one variable). `Diff` returns an unsimplified tree.

Techniques that `Technique` can name:

- greatest common factor, when the coefficients share a factor or the variables share a power
- difference of two squares, `a^2 - b^2 = (a - b)(a + b)`
- difference of cubes, `a^3 - b^3 = (a - b)(a^2 + ab + b^2)`
- sum of two cubes, `a^3 + b^3 = (a + b)(a^2 - ab + b^2)`
- perfect square trinomial, `a^2 ± 2ab + b^2 = (a ± b)^2`
- trinomial, for any other three-term quadratic
- factoring by grouping, for four terms
- rational roots, for anything else

## Not supported

- Division by anything that is not a number in polynomial operations, such as `1/x` or `x/y`. `Eval` and `Diff` do handle `1/x`.
- Variable or non-whole exponents in polynomial operations, such as `x^y`, `x^(1/2)`, or `x^-1`. `Diff` requires the exponent to be constant.
- Functions, roots, absolute values, equations, and inequalities.
- Factoring when more than one variable remains after the common monomial is removed. `x^2 + y` is rejected.
- Splitting a leftover polynomial of degree 2 or more into quadratic factors when it has no rational roots. Factoring stops there, so `(x^2+1)(x^2+2)` is reported as one irreducible factor.

## Worksheet bank

`bank.go` holds the problems from the screenshots in `equations/`, in file order. Each entry has the ASCII input, the operation, and the worksheet answer. `TestWorksheetBank` solves every entry and checks the result against that answer. The degree is checked where the worksheet asks for it. Factored answers are compared in canonical form, so the order of factors does not matter.

The TUI's bank view (ctrl+b) lists the same entries. Running one shows whether GoMath's answer matches the worksheet.

## Textbook coverage

This maps the concepts in *Algebra Essentials* (Blitzer) to what GoMath can process. Yes means GoMath handles the concept directly. Partly means it handles some of the concept or one of its steps. No means it is outside the system today.

### Chapter P: Fundamentals

| Concept | Status | Notes |
| --- | --- | --- |
| Algebraic expressions and evaluating them at values | Yes | `Eval` with a variable table. |
| Real-number classes and the models behind word problems | No | Word problems are not parsed. |
| Integer exponents and the exponent laws | Yes | Expanded by `Simplify`, with whole-number exponents only. |
| Scientific notation | No | Decimal input is accepted, but not `a × 10^n`. |
| Radicals and rational exponents | No | No roots and no fractional exponents. |
| Adding, subtracting, and multiplying polynomials | Yes | `Simplify`, with standard form and degree. |
| Special products (binomial squares, sum and difference of cubes) | Yes | Expanded by `Simplify`. |
| Factoring: greatest common factor | Yes | `Factor`, and `Technique` names it. |
| Factoring: grouping | Partly | The factored result is correct; `Technique` names the method only for four-term inputs. |
| Factoring: difference of squares, perfect square trinomials, trinomials | Yes | Found by rational roots; `Technique` names each. |
| Factoring: sum and difference of cubes | Yes | Found by rational roots; `Technique` names them. |
| Rational expressions: simplifying, multiplying, dividing | No | Division by a variable is not a polynomial operation. |

### Chapter 1: Equations and inequalities

| Concept | Status | Notes |
| --- | --- | --- |
| Graphs and graphing utilities | No | No graphing. |
| Solving linear and rational equations | No | There is no solve operation. |
| Models and applications | No | Word problems are not parsed. |
| Complex numbers, including `i` | No | `i` is read as a variable. |
| Solving quadratic equations by factoring | Partly | `Factor` gives the factors, and solving them is left to you. |
| Quadratic formula and completing the square | No | |
| Other equations: radical, absolute value, quadratic in form | No | |
| Linear and absolute value inequalities | No | |

### Chapter 2: Functions and graphs

| Concept | Status | Notes |
| --- | --- | --- |
| Function notation `f(x)`, domain, and range | No | |
| Graphs, slope, and linear functions | No | |
| Transformations of graphs | No | |
| Combining functions and composition | No | |
| Inverse functions | No | |
| Distance and midpoint formulas, circles | No | Needs square roots. |

### Chapter 3: Polynomial and rational functions

| Concept | Status | Notes |
| --- | --- | --- |
| Quadratic functions: vertex form, completing the square | No | Factoring works; the vertex form does not. |
| Graphs of polynomial functions, end behavior | No | |
| Dividing polynomials | No | No division operation. |
| Remainder and factor theorems | Partly | Factors are found when they are rational roots; remainders are not computed. |
| Zeros of polynomials: rational zeros | Partly | Rational roots are found; irrational and complex zeros are not. |
| Rational functions and their graphs | No | |
| Polynomial and rational inequalities | No | |
| Modeling with variation | No | |

### Chapters 4 and 5, and the appendix

| Concept | Status | Notes |
| --- | --- | --- |
| Exponential and logarithmic functions and equations | No | |
| Systems of linear and nonlinear equations | No | |
| Partial fractions | No | |
| Systems of inequalities and linear programming | No | |
| Proofs in the appendix | No | |

### Beyond the textbook

`Diff` is not in this book's table of contents. It differentiates with respect to one variable using the sum, product, quotient, and power rules, and `Eval` evaluates expressions. Both are listed under Operations above.
