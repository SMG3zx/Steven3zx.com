---@param a integer
---@param b integer
---@return integer
function gcd(a, b)
    while b ~= 0 do
        local t = a % b
        a = b
        b = t
    end
    return a
end

---@param n integer
---@return integer
function sum_to(n)
    local s = 0
    for i = 0, n do
        s = s + i
    end
    return s
end

---@param n integer
---@return integer
function fact(n)
    if n <= 1 then return 1 end
    return n * fact(n - 1)
end

---@param n integer
---@return integer
function collatz(n)
    local steps = 0
    while n ~= 1 do
        if n % 2 == 0 then
            n = n // 2
        else
            n = 3 * n + 1
        end
        steps = steps + 1
    end
    return steps
end

---@param x number
---@param y number
---@return number
function mean2(x, y)
    return (x + y) / 2
end

---@param n integer
---@return integer
function is_prime(n)
    if n < 2 then return 0 end
    local i = 2
    while i * i <= n do
        if n % i == 0 then return 0 end
        i = i + 1
    end
    return 1
end

---@param x integer
---@return integer
function square(x)
    return x * x
end

---@param n integer
---@return integer
function sumsq(n)
    local s = 0
    for i = 0, n - 1 do
        s = s + square(i)
    end
    return s
end

---@param a integer
---@param b integer
---@return integer
function abs_diff(a, b)
    local d = 0
    if a > b then
        d = a - b
    else
        d = b - a
    end
    return d
end

---@param base integer
---@param exp integer
---@return integer
function power(base, exp)
    local r = 1
    for k = 1, exp do
        r = r * base
    end
    return r
end
