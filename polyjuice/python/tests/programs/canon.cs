using System;

public static class Canon {
    public static int gcd(int a, int b) {
        while (b != 0) {
            int t = a % b;
            a = b;
            b = t;
        }
        return a;
    }

    public static int sum_to(int n) {
        int s = 0;
        for (int i = 0; i <= n; i++) {
            s += i;
        }
        return s;
    }

    public static int fact(int n) {
        if (n <= 1) return 1;
        return n * fact(n - 1);
    }

    public static int collatz(int n) {
        int steps = 0;
        while (n != 1) {
            if (n % 2 == 0) {
                n = n / 2;
            } else {
                n = 3 * n + 1;
            }
            steps++;
        }
        return steps;
    }

    public static double mean2(double x, double y) {
        return (x + y) / 2;
    }

    public static int is_prime(int n) {
        if (n < 2) return 0;
        int i = 2;
        while (i * i <= n) {
            if (n % i == 0) return 0;
            i++;
        }
        return 1;
    }

    public static int square(int x) {
        return x * x;
    }

    public static int sumsq(int n) {
        int s = 0;
        for (int i = 0; i < n; i++) {
            s += square(i);
        }
        return s;
    }

    public static int abs_diff(int a, int b) {
        int d;
        if (a > b) {
            d = a - b;
        } else {
            d = b - a;
        }
        return d;
    }

    public static int power(int b0, int e0) {
        int r = 1;
        for (int k = 0; k < e0; k++) {
            r *= b0;
        }
        return r;
    }
}
