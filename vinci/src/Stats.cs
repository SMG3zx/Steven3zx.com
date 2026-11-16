namespace Vinci;

/// Shewhart's control limits for a process: is it behaving as usual, or has
/// something special happened?
public static class Stats
{
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
}
