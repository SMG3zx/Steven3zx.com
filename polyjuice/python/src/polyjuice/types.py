"""Annotations understood by the Python-subset frontend.

`int` means a 32-bit signed integer with wraparound. `Array[int, N]` is a
fixed-size array of N such integers.
"""


class Array:
    def __class_getitem__(cls, item):
        return cls
