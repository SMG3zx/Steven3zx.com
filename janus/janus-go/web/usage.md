# Web.App Usage

```golang
app := web.New("")

app.Get("/", func(c *web.Context) any { return "hello" })
app.Get("/hi", func(c *web.Context) any { return "hi" })

// Static, dynamic, and wildcard. Static wins, then dynamic, thenwildcard.
app.Get("/id/1", func(c *web.Context) any { return "static path" })
app.Get("/id/:id", func(c *web.Context) any { return c.Param("id") })
app.Get("/files/*", func(c *web.Context) any { return c.Param("*") })

// Optional param: matches both /opt and /opt/:name
app.Get("/opt/:name?", func(c *web.Context) any {
    return "name=" + c.Param("name")
})

app.Post("/echo", func(c *web.Context) any {
    return map[string]string{"ok": "true"} // JSON response
})

app.All("/ping", func(c *web.Context) any { return "pong" })
app.Route("M-SEARCH", "/m-search", func(c *web.Context) any { return "searched" })

// Groups share a prefix; a prefixed App can also be built and merged in.
app.Group("/user", func(u *web.App) {
    u.Post("/sign-in", func(c *web.Context) any { return "Sign in" })
    u.Post("/sign-up", func(c *web.Context) any { return "Sign up" })
    u.Post("/profile", func(c *web.Context) any { return "Profile" })
})

admin := web.New("/admin")
admin.Get("/stats", func(c *web.Context) any { return "stats" })
app.Use(admin)

log.Fatal(app.Listen(":3000"))
```
