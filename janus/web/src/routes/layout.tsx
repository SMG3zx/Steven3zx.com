import { component$, Slot } from "@builder.io/qwik";

/** The header and footer every page shares. */
export default component$(() => {
  return (
    <>
      <a class="skip-link" href="#main">
        Skip to content
      </a>
      <header class="site-header">
        <a class="brand" href="/">
          Janus
        </a>
        <nav class="site-nav" aria-label="Main">
          <a href="/projects">Projects</a>
          <a href="/agents">Agents</a>
          <a href="/account">Account</a>
        </nav>
      </header>
      <div id="main">
        <Slot />
      </div>
      <footer class="site-footer">Janus</footer>
    </>
  );
});
