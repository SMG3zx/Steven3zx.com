fun gcd(a0: Int, b0: Int): Int {
    var a = a0
    var b = b0
    while (b != 0) {
        val t = a % b
        a = b
        b = t
    }
    return a
}

fun sum_to(n: Int): Int {
    var s = 0
    for (i in 0..n) {
        s += i
    }
    return s
}

fun fact(n: Int): Int {
    if (n <= 1) return 1
    return n * fact(n - 1)
}

fun collatz(n0: Int): Int {
    var n = n0
    var steps = 0
    while (n != 1) {
        if (n % 2 == 0) {
            n = n / 2
        } else {
            n = 3 * n + 1
        }
        steps++
    }
    return steps
}

fun mean2(x: Double, y: Double): Double {
    return (x + y) / 2
}

fun is_prime(n: Int): Int {
    if (n < 2) return 0
    var i = 2
    while (i * i <= n) {
        if (n % i == 0) return 0
        i++
    }
    return 1
}

fun square(x: Int): Int = x * x

fun sumsq(n: Int): Int {
    var s = 0
    for (i in 0 until n) {
        s += square(i)
    }
    return s
}

fun abs_diff(a: Int, b: Int): Int {
    var d = 0
    if (a > b) {
        d = a - b
    } else {
        d = b - a
    }
    return d
}

fun power(base: Int, exp: Int): Int {
    var r = 1
    for (k in 0 until exp) {
        r *= base
    }
    return r
}
