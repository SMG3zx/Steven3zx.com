import { component$ } from "@builder.io/qwik";
import { hostOf, previewOf, type SavedLink } from "../../links";

interface Props {
  link: SavedLink;
}

export const LinkCard = component$<Props>(({ link }) => {
  const preview = previewOf(link);

  return (
    <li class="card">
      <a href={link.url} target="_blank" rel="noopener noreferrer">
        <span class="title">{link.title}</span>
        <span class="host">{hostOf(link.url)}</span>
      </a>
      {preview && (
        <img
          class="preview"
          src={preview}
          alt={`Screenshot of ${link.title}`}
          width={1200}
          height={750}
          loading="lazy"
        />
      )}
      {link.description && <p class="description">{link.description}</p>}
      {link.tags.length > 0 && (
        <ul class="card-tags">
          {link.tags.map((tag) => (
            <li key={tag}>{tag}</li>
          ))}
        </ul>
      )}
    </li>
  );
});
