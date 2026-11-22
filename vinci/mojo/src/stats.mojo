# Shewhart's control limits for a process. The sums run left to right, so the
# limits are stable to the last digit.


@fieldwise_init
struct Limits(Copyable, Movable):
    """The centre and the two control limits of a series of measurements."""

    var center: Float64
    var lower: Float64
    var upper: Float64

    def holds(self, x: Float64) -> Bool:
        """True when x is ordinary variation: inside the limits, both included.
        """
        return x >= self.lower and x <= self.upper


def mean(xs: List[Float64]) -> Float64:
    """Stats.Mean: the sum added in order, divided by the count."""
    var total = 0.0
    for x in xs:
        total += x
    return total / Float64(len(xs))


def control_limits(xs: List[Float64]) -> Optional[Limits]:
    """Limits for single measurements: the mean, plus or minus 2.66 times the
    average step between neighbours. None with fewer than two points."""
    if len(xs) < 2:
        return None
    var center = mean(xs)
    var total = 0.0
    for i in range(len(xs) - 1):
        total += abs(xs[i + 1] - xs[i])
    var average_step = total / Float64(len(xs) - 1)
    return Limits(
        center,
        center - 2.66 * average_step,
        center + 2.66 * average_step,
    )
