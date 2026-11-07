function gcd {
    [OutputType([int])]
    param([int]$a, [int]$b)
    while ($b -ne 0) {
        $t = $a % $b
        $a = $b
        $b = $t
    }
    return $a
}

function sum_to {
    [OutputType([int])]
    param([int]$n)
    $s = 0
    for ($i = 0; $i -le $n; $i++) {
        $s += $i
    }
    return $s
}

function fact {
    [OutputType([int])]
    param([int]$n)
    if ($n -le 1) { return 1 }
    return $n * (fact ($n - 1))
}

function collatz {
    [OutputType([int])]
    param([int]$n)
    $steps = 0
    while ($n -ne 1) {
        if ($n % 2 -eq 0) {
            $h = 0
            while ($h * 2 -lt $n) { $h++ }
            $n = $h
        } else {
            $n = 3 * $n + 1
        }
        $steps++
    }
    return $steps
}

function mean2 {
    [OutputType([double])]
    param([double]$x, [double]$y)
    return ($x + $y) / 2.0
}

function is_prime {
    [OutputType([int])]
    param([int]$n)
    if ($n -lt 2) { return 0 }
    $i = 2
    while ($i * $i -le $n) {
        if ($n % $i -eq 0) { return 0 }
        $i++
    }
    return 1
}

function square {
    [OutputType([int])]
    param([int]$x)
    return $x * $x
}

function sumsq {
    [OutputType([int])]
    param([int]$n)
    $s = 0
    for ($i = 0; $i -lt $n; $i++) {
        $s += square $i
    }
    return $s
}

function abs_diff {
    [OutputType([int])]
    param([int]$a, [int]$b)
    $d = 0
    if ($a -gt $b) {
        $d = $a - $b
    } else {
        $d = $b - $a
    }
    return $d
}

function power {
    [OutputType([int])]
    param([int]$base, [int]$exp)
    $r = 1
    for ($k = 0; $k -lt $exp; $k++) {
        $r *= $base
    }
    return $r
}
