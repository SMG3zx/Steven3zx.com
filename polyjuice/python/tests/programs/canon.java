public class Canon {
    static int gcd(int a, int b) {
        while (b != 0) {
            int t = a % b;
            a = b;
            b = t;
        }
        return a;
    }

    static int sum_to(int n) {
        int s = 0;
        for (int i = 0; i <= n; i++) {
            s += i;
        }
        return s;
    }

    static int fact(int n) {
        if (n <= 1) return 1;
        return n * fact(n - 1);
    }

    static int collatz(int n) {
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

    static double mean2(double x, double y) {
        return (x + y) / 2;
    }

    static int is_prime(int n) {
        if (n < 2) return 0;
        int i = 2;
        while (i * i <= n) {
            if (n % i == 0) return 0;
            i++;
        }
        return 1;
    }

    static int square(int x) {
        return x * x;
    }

    static int sumsq(int n) {
        int s = 0;
        for (int i = 0; i < n; i++) {
            s += square(i);
        }
        return s;
    }

    static int abs_diff(int a, int b) {
        int d;
        if (a > b) {
            d = a - b;
        } else {
            d = b - a;
        }
        return d;
    }

    static int power(int base, int exp) {
        int r = 1;
        for (int k = 0; k < exp; k++) {
            r *= base;
        }
        return r;
    }
}
