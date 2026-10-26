import { component$, Slot } from "@builder.io/qwik";
import { Link } from "@builder.io/qwik-city";

export const Topbar = component$(() => {
  return (
    <header class="topbar">
      <h1>
        <Link href="/">Firecracker Manager</Link>
      </h1>
      <Slot />
    </header>
  );
});
