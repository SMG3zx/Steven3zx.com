import { component$, useComputed$, useSignal } from "@builder.io/qwik";
import { routeLoader$, type DocumentHead } from "@builder.io/qwik-city";
import { LinkCard } from "../components/link-card/link-card";
import { discoverLocal, localPortOf } from "../discover";
import { links as saved, tagsOf } from "../links";

/**
 * Saved links plus every web page served on this machine right now. A saved link
 * that points at a local port wins over the discovered one, since it says more.
 */
export const useLinks = routeLoader$(async ({ url }) => {
  const covered = saved.flatMap((link) => {
    const port = localPortOf(link.url);
    return port === undefined ? [] : [port];
  });
  const own = Number(url.port) || (url.protocol === "https:" ? 443 : 80);
  return [...saved, ...(await discoverLocal([own, ...covered]))];
});

export default component$(() => {
  const links = useLinks();
  const allTags = tagsOf(links.value);
  const query = useSignal("");
  const activeTag = useSignal<string | null>(null);

  const visible = useComputed$(() => {
    const q = query.value.trim().toLowerCase();
    return links.value.filter((link) => {
      if (activeTag.value && !link.tags.includes(activeTag.value)) {
        return false;
      }
      if (!q) {
        return true;
      }
      return [link.title, link.url, link.description ?? "", ...link.tags]
        .join(" ")
        .toLowerCase()
        .includes(q);
    });
  });

  return (
    <main class="portal">
      <header>
        <h1>Portal</h1>
        <p class="count">
          {visible.value.length} of {links.value.length} links
        </p>
      </header>

      <input
        class="search"
        type="search"
        placeholder="Search links, tags, descriptions…"
        aria-label="Search links"
        value={query.value}
        onInput$={(_, el) => (query.value = el.value)}
      />

      <nav class="tags" aria-label="Filter by tag">
        <button
          type="button"
          class={{ tag: true, active: activeTag.value === null }}
          onClick$={() => (activeTag.value = null)}
        >
          all
        </button>
        {allTags.map((tag) => (
          <button
            key={tag}
            type="button"
            class={{ tag: true, active: activeTag.value === tag }}
            onClick$={() =>
              (activeTag.value = activeTag.value === tag ? null : tag)
            }
          >
            {tag}
          </button>
        ))}
      </nav>

      {visible.value.length === 0 ? (
        <p class="empty">No links match.</p>
      ) : (
        <ul class="links">
          {visible.value.map((link) => (
            <LinkCard key={link.url} link={link} />
          ))}
        </ul>
      )}
    </main>
  );
});

export const head: DocumentHead = {
  title: "Portal",
  meta: [
    {
      name: "description",
      content: "Saved links, searchable and filterable by tag.",
    },
  ],
};
