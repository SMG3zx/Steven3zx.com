const feed = document.querySelector('#feed');
const render = (posts) => { feed.innerHTML = posts.map((p) => `<article class="post"><b>${p.author}</b> <span class="meta">@${p.handle}</span><p>${p.text}</p><button class="like" data-id="${p.id}">♥ ${p.likes}</button></article>`).join(''); };
const load = async (url = '/api/feed') => render((await (await fetch(url)).json()).posts);
document.querySelector('#search').oninput = (event) => load(`/api/search?q=${encodeURIComponent(event.target.value)}`);
document.querySelector('#composer').onsubmit = async (event) => { event.preventDefault(); const textarea = event.target.querySelector('textarea'); await fetch('/api/posts', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ text: textarea.value }) }); textarea.value = ''; load(); };
feed.onclick = async (event) => { if (!event.target.matches('.like')) return; await fetch(`/api/posts/${event.target.dataset.id}/like`, { method: 'POST' }); load(); };
load();
