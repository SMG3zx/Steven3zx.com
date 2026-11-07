<?php

function gcd(int $a, int $b): int {
    while ($b != 0) {
        $t = $a % $b;
        $a = $b;
        $b = $t;
    }
    return $a;
}

function sum_to(int $n): int {
    $s = 0;
    for ($i = 0; $i <= $n; $i++) {
        $s += $i;
    }
    return $s;
}

function fact(int $n): int {
    if ($n <= 1) return 1;
    return $n * fact($n - 1);
}

function collatz(int $n): int {
    $steps = 0;
    while ($n != 1) {
        if ($n % 2 == 0) {
            $n = intdiv($n, 2);
        } else {
            $n = 3 * $n + 1;
        }
        $steps++;
    }
    return $steps;
}

function mean2(float $x, float $y): float {
    return ($x + $y) / 2.0;
}

function is_prime(int $n): int {
    if ($n < 2) return 0;
    $i = 2;
    while ($i * $i <= $n) {
        if ($n % $i == 0) return 0;
        $i++;
    }
    return 1;
}

function square(int $x): int {
    return $x * $x;
}

function sumsq(int $n): int {
    $s = 0;
    for ($i = 0; $i < $n; $i++) {
        $s += square($i);
    }
    return $s;
}

function abs_diff(int $a, int $b): int {
    $d = 0;
    if ($a > $b) {
        $d = $a - $b;
    } else {
        $d = $b - $a;
    }
    return $d;
}

function power(int $base, int $exp): int {
    $r = 1;
    for ($k = 0; $k < $exp; $k++) {
        $r *= $base;
    }
    return $r;
}
