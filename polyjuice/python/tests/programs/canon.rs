fn gcd(mut a: i32, mut b: i32) -> i32 {
    while b != 0 {
        let t = a % b;
        a = b;
        b = t;
    }
    a
}

fn sum_to(n: i32) -> i32 {
    let mut s = 0;
    for i in 0..=n {
        s += i;
    }
    s
}

fn fact(n: i32) -> i32 {
    if n <= 1 {
        return 1;
    }
    n * fact(n - 1)
}

fn collatz(mut n: i32) -> i32 {
    let mut steps = 0;
    while n != 1 {
        if n % 2 == 0 {
            n = n / 2;
        } else {
            n = 3 * n + 1;
        }
        steps += 1;
    }
    steps
}

fn mean2(x: f64, y: f64) -> f64 {
    (x + y) / 2.0
}

fn is_prime(n: i32) -> i32 {
    if n < 2 {
        return 0;
    }
    let mut i = 2;
    while i * i <= n {
        if n % i == 0 {
            return 0;
        }
        i += 1;
    }
    1
}

fn square(x: i32) -> i32 {
    x * x
}

fn sumsq(n: i32) -> i32 {
    let mut s = 0;
    for i in 0..n {
        s += square(i);
    }
    s
}

fn abs_diff(a: i32, b: i32) -> i32 {
    if a > b {
        a - b
    } else {
        b - a
    }
}

fn power(base: i32, exp: i32) -> i32 {
    let mut r = 1;
    for _k in 0..exp {
        r *= base;
    }
    r
}
