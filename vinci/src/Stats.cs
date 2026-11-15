namespace Vinci;

/// The Six Sigma arithmetic: process behaviour (control limits) and process
/// capability (defects per million opportunities, sigma level).
public static class Stats
{
    /// Below this many measurements a sigma level is noise, so none is given.
    public const int MinForSigmaLevel = 30;

    public sealed record Limits(double Center, double Lower, double Upper)
    {
        public bool Holds(double x) => x >= Lower && x <= Upper;
    }

    public static double Mean(IReadOnlyList<double> xs) => xs.Sum() / xs.Count;

    /// Control limits for single measurements (Shewhart's individuals chart):
    /// the mean, plus or minus 2.66 times the average step between neighbours.
    /// 2.66 is 3 / 1.128, where 1.128 turns an average range of two points
    /// into one standard deviation. A point outside the limits has a special
    /// cause worth finding; a point inside is ordinary noise, so leave it alone.
    public static Limits? ControlLimits(IReadOnlyList<double> xs)
    {
        if (xs.Count < 2)
            return null;
        var center = Mean(xs);
        var averageStep = xs.Zip(xs.Skip(1), (a, b) => Math.Abs(b - a)).Average();
        return new Limits(center, center - 2.66 * averageStep, center + 2.66 * averageStep);
    }

    public static double Dpmo(int defects, int opportunities) => 1e6 * defects / opportunities;

    /// The sigma level as Six Sigma states it: the normal quantile of the
    /// yield, plus the customary 1.5 sigma allowance for long-term drift.
    /// So 3.4 defects per million is "six sigma". Infinite with no defects.
    public static double SigmaLevel(int defects, int opportunities)
    {
        if (defects == 0)
            return double.PositiveInfinity;
        if (defects >= opportunities)
            return 0;
        return Math.Max(0, NormalQuantile(1 - (double)defects / opportunities) + 1.5);
    }

    /// Inverse of the standard normal CDF, by Peter Acklam's rational
    /// approximation (relative error under 1.15e-9).
    public static double NormalQuantile(double p)
    {
        if (p <= 0 || p >= 1)
            throw new ArgumentOutOfRangeException(nameof(p));

        double[] a =
        [
            -3.969683028665376e+01,
            2.209460984245205e+02,
            -2.759285104469687e+02,
            1.383577518672690e+02,
            -3.066479806614716e+01,
            2.506628277459239e+00,
        ];
        double[] b =
        [
            -5.447609879822406e+01,
            1.615858368580409e+02,
            -1.556989798598866e+02,
            6.680131188771972e+01,
            -1.328068155288572e+01,
        ];
        double[] c =
        [
            -7.784894002430293e-03,
            -3.223964580411365e-01,
            -2.400758277161838e+00,
            -2.549732539343734e+00,
            4.374664141464968e+00,
            2.938163982698783e+00,
        ];
        double[] d =
        [
            7.784695709041462e-03,
            3.224671290700398e-01,
            2.445134137142996e+00,
            3.754408661907416e+00,
        ];
        const double low = 0.02425;

        if (p < low)
        {
            var q = Math.Sqrt(-2 * Math.Log(p));
            return (((((c[0] * q + c[1]) * q + c[2]) * q + c[3]) * q + c[4]) * q + c[5])
                / ((((d[0] * q + d[1]) * q + d[2]) * q + d[3]) * q + 1);
        }
        if (p > 1 - low)
            return -NormalQuantile(1 - p);

        var x = p - 0.5;
        var r = x * x;
        return (((((a[0] * r + a[1]) * r + a[2]) * r + a[3]) * r + a[4]) * r + a[5])
            * x
            / (((((b[0] * r + b[1]) * r + b[2]) * r + b[3]) * r + b[4]) * r + 1);
    }
}
