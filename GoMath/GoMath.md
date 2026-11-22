https://medium.com/better-programming/building-a-computer-algebra-system-in-go-part-1-multivariate-expressions-and-differentiation-291d948aa0e4

# GoMath

GoMath parses algebra written in ASCII or TeX, shows each parsing and algebra step, and checks its answers against the worksheets in `equations/`.

Run the TUI with `go run .` in this directory. Tests run with `go test ./...`.

## Input

### ASCII

| Element | Examples | Notes |
| --- | --- | --- |
| Whole numbers | `12`, `-4` | Polynomial operations need whole-number coefficients. |
| Decimal numbers | `3.5` | Read as the exact fraction `7/2` by polynomial operations. Simplify writes the answer in decimals when it can, such as `0.49x + 6`. |
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

Other TeX commands such as `\sin`, `\pi`, subscripts, and `\begin{...}` are not supported.

## Operations

| Operation | TUI key | What it does | Example |
| --- | --- | --- | --- |
| Simplify | enter | Combines like terms and writes the standard form with its degree. | `(x+4)(x^2-4x+16)` gives `x^3 + 64`, degree 3. |
| Factor | ctrl+f | Pulls out the integer content and common variable powers, then finds rational roots. | `16x + 40` gives `8(2x + 5)`. |
| Technique | ctrl+t | Names the factoring method that fits the shape. | `64x^3 - 125` gives `difference of cubes`. |
| Prime check | ctrl+p | Reports whether the polynomial is irreducible over the integers. | `x^2 - 2x - 2` gives `true`. |
| Radical form | ctrl+g | Rewrites `a^(m/n)` as a radical without evaluating it. Also accepts `169^{\frac{1}{2}}`. | `169^(1/2)` gives `sqrt(169)`. `8^(2/3)` gives `sqrt[3](8^2)`. |
| Solve | ctrl+s | Solves an equation in one variable and writes the solution set. Handles linear equations and quadratics with rational roots. | `7x - 8 = 55` gives `{9}`. `x^2 - 5x + 6 = 0` gives `{2, 3}`. |
| Excluded numbers | ctrl+d | Lists the numbers a rational expression must exclude from its domain, which are the roots of its denominators. Answers `all real numbers` when no denominator can reach 0. | `4/(x + 6)` gives `{-6}`. `1/(x^2 + 4)` gives `all real numbers`. |
| Inequality | ctrl+o | Solves a linear or compound inequality and writes interval notation. | `2x + 4 > 10` gives `(3, ∞)`. `3 < 2x + 1 <= 9` gives `(1, 4]`. |
| Slope | ctrl+o | Gives the slope through two points and whether the line rises, falls, is horizontal, or is vertical. | `(6,2) and (7,6)` gives `4 (rises)`. |
| All line forms | ctrl+l | Writes a line in every form it has: point-slope, slope-intercept, and standard form `Ax + By = C` with whole numbers and A positive. Vertical lines give `x = a`. | `parallel to y = 3x through (2,-2)` gives `y + 2 = 3(x - 2)`, `y = 3x - 8`, and `3x - y = 8`. |
| Point-slope / slope-intercept | ctrl+o | Writes the line through two points, through a point with a given slope, or through a point parallel or perpendicular to `y = px/q`. | `slope 3 through (3,2)` gives `y - 2 = 3(x - 3)` and `y = 3x - 7`. `perpendicular to y = 2x through (2,4)` gives `y - 4 = (-1/2)(x - 2)`. |
| Graph | ctrl+e | Lists the points of `y = expression` and draws them on axes. Without `; x = a, b, c` it uses x from -3 to 3. | `y = x - 2` gives `(-3, -5), ... (3, 1)` and a plot. |
| Table of values | ctrl+o | Gives y for each listed x. | `y = x - 2; x = -3, -2, -1` gives `-5, -4, -3`. |
| Solve for a variable | ctrl+o | Isolates one variable of a formula that is linear in it. | `r = ba for b` gives `b = r/a`. |
| Variation | ctrl+o | Finds a value in a direct, inverse, or joint variation. Input has three parts, `relation; known values; find unknown when values`. Words are `directly`, `inversely`, and `jointly`, and `z^2` writes the square. | `y varies directly as x; y = 84 when x = 7; find y when x = 18` gives `216`. |
| System of equations | ctrl+o | Solves two linear equations in two variables by Cramer's rule. Gives `no solution` or `infinitely many solutions` when the lines do not cross once. | `x + y = 5; x - y = -15` gives `{(-5, 10)}`. |
| Check a solution | ctrl+o | Says whether an ordered pair satisfies every equation after `;`. | `(2,-6); y = 4x - 14; 2x + 4y = -20` gives `Yes`. |
| Elimination multiplier | ctrl+o | Answers the blank in the addition method. The input fixes one equation's multiplier and gives the other's, so the chosen variable cancels. | `x + 5y = 2; 5x + 4y = -11; eliminate x; multiply second by 1` gives `-5`. |
| Root blank | ctrl+r | Fills the blank in "root(n) = r because ___ = n" with `r^k`, where `k` is the index. `n` must be a perfect power. Accepts `sqrt(169)`, `sqrt[5](-3125)`, or TeX such as `\sqrt[5]{-3125}`. | `sqrt(169)` gives `13^2`. `sqrt[5](-3125)` gives `(-5)^5`. |

Keys with `ctrl+o` have no footer key. The all-line-forms operation is on ctrl+l, and the result view lists each form on its own line. Press ctrl+o in the input for the full list of operations. The footer keys are bright when their operation accepts the input, and dim when it does not. Pressing a dim key still runs it and shows why it fails.

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
- Functions, roots of anything other than a whole number or single term (see Radicals), absolute values, and inequalities. Equations are only solved through Solve.
- Polynomial equations of degree 3 or more whose roots are not rational, such as `x^3 = 2`. Solve finds rational roots only.
- Systems with more than two equations or three variables, and nonlinear systems.
- Factoring when more than one variable remains after the common monomial is removed. `x^2 + y` is rejected.
- Splitting a leftover polynomial of degree 2 or more into quadratic factors when it has no rational roots. Factoring stops there, so `(x^2+1)(x^2+2)` is reported as one irreducible factor.

## Radicals

Simplify accepts `sqrt(n)` and `\sqrt{n}` where the radicand is a single term, such as `175`, `6x`, or `12y^2`. Square factors come out (`sqrt(8)` is `2sqrt(2)`), perfect squares become numbers, and like radicals combine: `7sqrt(2) + 11sqrt(2)` gives `18sqrt(2)`. Roots of any index work with `sqrt[k](n)` or `\sqrt[k]{n}`: `sqrt[3](72)` gives `2sqrt[3](9)`. The product and quotient rules below join radicals of the same index, so `sqrt[3](49) * sqrt[3](14)` gives `7sqrt[3](2)`. The product rule joins neighbours first: `sqrt(6x) * sqrt(30x)` gives `6xsqrt(5)`. Variables are taken as nonnegative, so `sqrt(x^2)` is `x`. `•`, `\cdot`, and `*` all mean multiply. Each distinct radical is swapped for a stand-in letter, so the input should not rely on that letter being a variable. The quotient rule also joins `sqrt(A)/sqrt(B)` when `B` divides `A` exactly: `sqrt(147x^3) / sqrt(3x)` gives `7x`. It handles a root of a whole-number fraction too: `sqrt(1/144)` gives `1/12` and `sqrt(20/9)` gives `2sqrt(5)/3`. A radical left in the denominator would need rationalizing, which is not supported. Radicals are not supported with factor, technique, or prime check.

Evaluate also takes fractional exponents, so `169^(1/2)` gives `13`, and roots: `sqrt(100 - 36)` gives `8`, and `sqrt(36 - 100)` gives `not a real number`, matching worksheets that ask you to evaluate or say the expression is not real. Evaluate has no footer key. The ctrl+o list runs it on an input with no variables, and the bank runs it too.

## Fractional exponents

Simplify takes a power of a fractional power, `(x^(p/q))^r`, and multiplies the exponents: `(x^(6/7))^7` gives `x^6`, and `(x^(2/3))^(1/2)` gives `x^(1/3)`. Only this one shape works. Other expressions with fractional exponents, such as `x^(1/2) * x^(1/2)`, are not supported.

Solve also takes an equation with one radical: `sqrt(x + 4) = 8` gives `{60}`. It isolates the radical, raises both sides to the index, solves what is left, and checks each candidate in the original equation, so extraneous roots are dropped: `sqrt(2x + 3) = x` gives `{3}`, not `{-1, 3}`. The radicand may be any polynomial without parentheses inside, and an equation with two radicals is not supported.

Solve takes one more shape, `v^(p/q) = K` for a whole number `K`: `x^(5/2) = 243` gives `{9}`. It raises both sides to the power `q`, finds the whole-number roots of `v^p = K^q`, and checks each in the original equation, so `x^(3/2) = -8` gives `no solution`. An answer that is not a whole number is not supported.

Solve handles a polynomial of degree 3 or more when it has rational roots. It finds them one at a time with the rational root theorem and divides each out, so `2x^3 + 3x^2 = 8x + 12` gives `{-2, -3/2, 2}`.

## Worksheet bank

`bank.go` holds the problems from the screenshots in `equations/`, in file order. Each entry has the ASCII input, the operation, and the worksheet answer. `TestWorksheetBank` solves every entry and checks the result against that answer. The degree is checked where the worksheet asks for it. Factored answers are compared in canonical form, so the order of factors does not matter.

Problems 16, 17, and 25 are multiple choice. Each stores the system of the correct choice and checks its solution, not the letter.

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
