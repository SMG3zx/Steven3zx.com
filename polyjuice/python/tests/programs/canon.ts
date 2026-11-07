function gcd(a: number, b: number): number {
    while (b !== 0) {
        const t: number = a % b;
        a = b;
        b = t;
    }
    return a;
}

function sum_to(n: number): number {
    let s = 0;
    for (let i = 0; i <= n; i++) {
        s += i;
    }
    return s;
}

function fact(n: number): number {
    if (n <= 1) return 1;
    return n * fact(n - 1);
}

function collatz(n: number): number {
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

function mean2(x: number, y: number): number {
    return (x + y) / 2;
}

function is_prime(n: number): number {
    if (n < 2) return 0;
    let i = 2;
    while (i * i <= n) {
        if (n % i === 0) return 0;
        i++;
    }
    return 1;
}

function square(x: number): number {
    return x * x;
}

function sumsq(n: number): number {
    let s = 0;
    for (let i = 0; i < n; i++) {
        s += square(i);
    }
    return s;
}

function abs_diff(a: number, b: number): number {
    let d = 0;
    if (a > b) {
        d = a - b;
    } else {
        d = b - a;
    }
    return d;
}

function power(base: number, exp: number): number {
    let r = 1;
    for (let k = 0; k < exp; k++) {
        r *= base;
    }
    return r;
}
