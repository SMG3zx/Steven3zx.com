function gcd(a, b) {
    while (b !== 0) {
        const t = a % b;
        a = b;
        b = t;
    }
    return a;
}

function sum_to(n) {
    let s = 0;
    for (let i = 0; i <= n; i++) {
        s += i;
    }
    return s;
}

function fact(n) {
    if (n <= 1) return 1;
    return n * fact(n - 1);
}

function collatz(n) {
    let steps = 0;
    while (n !== 1) {
        if (n % 2 === 0) {
            n = n / 2;
        } else {
            n = 3 * n + 1;
        }
        steps++;
    }
    return steps;
}

function mean2(x, y) {
    return (x + y) / 2;
}

function is_prime(n) {
    if (n < 2) return 0;
    let i = 2;
    while (i * i <= n) {
        if (n % i === 0) return 0;
        i++;
    }
    return 1;
}

function square(x) {
    return x * x;
}

function sumsq(n) {
    let s = 0;
    for (let i = 0; i < n; i++) {
        s += square(i);
    }
    return s;
}

function abs_diff(a, b) {
    let d = 0;
    if (a > b) {
        d = a - b;
    } else {
        d = b - a;
    }
    return d;
}

function power(base, exp) {
    let r = 1;
    for (let k = 0; k < exp; k++) {
        r *= base;
    }
    return r;
}
