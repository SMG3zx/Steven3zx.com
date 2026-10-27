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

`bank.go` holds the 35 problems from the screenshots in `equations/`, in file order. Each entry has the ASCII input, the operation, and the worksheet answer. `TestWorksheetBank` solves every entry and checks the result against that answer. The degree is checked where the worksheet asks for it. Factored answers are compared in canonical form, so the order of factors does not matter.

The TUI's bank view (ctrl+b) lists the same entries. Running one shows whether GoMath's answer matches the worksheet.
