"""Shewhart's control limits for a process. The sums run left to right, so the limits are
stable to the last digit.

Port of mojo/src/stats.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Limits:
    """The centre and the two control limits of a series of measurements."""

    center: float
    lower: float
    upper: float

    def holds(self, x: float) -> bool:
        """True when x is ordinary variation: inside the limits, both included."""
        return self.lower <= x <= self.upper


def mean(xs: list[float]) -> float:
    """Stats.Mean: the sum added in order, divided by the count."""
    total = 0.0
    for x in xs:
        total += x
    if len(xs) == 0:
        return float("nan")
    return total / float(len(xs))


def control_limits(xs: list[float]) -> Limits | None:
    """Limits for single measurements: the mean, plus or minus 2.66 times the average step
    between neighbours. None with fewer than two points."""
    if len(xs) < 2:
        return None
    center = mean(xs)
    total = 0.0
    for i in range(len(xs) - 1):
        total += abs(xs[i + 1] - xs[i])
    average_step = total / float(len(xs) - 1)
    return Limits(center, center - 2.66 * average_step, center + 2.66 * average_step)
