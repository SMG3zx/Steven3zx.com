package main

func gcd(a int, b int) int {
	for b != 0 {
		t := a % b
		a = b
		b = t
	}
	return a
}

func sum_to(n int) int {
	s := 0
	for i := 0; i <= n; i++ {
		s += i
	}
	return s
}

func fact(n int) int {
	if n <= 1 {
		return 1
	}
	return n * fact(n-1)
}

func collatz(n int) int {
	steps := 0
	for n != 1 {
		if n%2 == 0 {
			n = n / 2
		} else {
			n = 3*n + 1
		}
		steps++
	}
	return steps
}

func mean2(x float64, y float64) float64 {
	return (x + y) / 2
}

func is_prime(n int) int {
	if n < 2 {
		return 0
	}
	i := 2
	for i*i <= n {
		if n%i == 0 {
			return 0
		}
		i++
	}
	return 1
}

func square(x int) int {
	return x * x
}

func sumsq(n int) int {
	s := 0
	for i := range n {
		s += square(i)
	}
	return s
}

func abs_diff(a int, b int) int {
	var d int
	if a > b {
		d = a - b
	} else {
		d = b - a
	}
	return d
}

func power(base int, exp int) int {
	r := 1
	for k := 0; k < exp; k++ {
		r *= base
	}
	return r
}
