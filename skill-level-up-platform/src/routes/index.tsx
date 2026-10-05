import { component$, useStore, useVisibleTask$ } from '@builder.io/qwik';
import { DocumentHead } from '@builder.io/qwik-city';

type Quest = { icon: string; title: string; repo: string; tags: string[]; difficulty: string; level: string; time: string; xp: string; logoStyle: string; difficultyStyle: string };

const quests: Quest[] = [
  { icon: 'F', title: 'Handle cancelled requests gracefully', repo: 'fastapi / fastapi · #11642', tags: ['Python', 'AsyncIO', 'Testing'], difficulty: 'Intermediate', level: '7/20', time: '2–4 hours', xp: '+420 XP', logoStyle: 'border-[#37414a] bg-[#17261d] text-[#7cf0a2]', difficultyStyle: 'text-orange' },
  { icon: '⚛', title: 'Improve error boundary recovery state', repo: 'facebook / react · #30912', tags: ['TypeScript', 'React', 'Docs'], difficulty: 'Intermediate', level: '8/20', time: '3–5 hours', xp: '+510 XP', logoStyle: 'border-[#37414a] bg-[#13232d] text-[#61dafb]', difficultyStyle: 'text-cyan' },
  { icon: 'R', title: 'Add shell completion for config commands', repo: 'sharkdp / fd · #1421', tags: ['Rust', 'CLI', 'DX'], difficulty: 'Stretch', level: '11/20', time: '4–6 hours', xp: '+680 XP', logoStyle: 'border-[#37414a] bg-[#2d1d15] text-[#ffa476]', difficultyStyle: 'text-purple' },
  { icon: 'P', title: 'Document connection pool timeout behavior', repo: 'postgres / postgres · #21871', tags: ['PostgreSQL', 'Docs'], difficulty: 'Beginner', level: '4/20', time: '1–2 hours', xp: '+180 XP', logoStyle: 'border-[#37414a] bg-[#18281f] text-[#73d59b]', difficultyStyle: 'text-lime' },
];

const tagStyle = (tag: string) => tag === 'Python' || tag === 'AsyncIO' || tag === 'PostgreSQL' ? 'bg-[#24301b] text-[#b8da7a]' : tag === 'Testing' || tag === 'TypeScript' || tag === 'React' ? 'bg-[#172932] text-[#8bd9fa]' : 'bg-[#242b32] text-[#aeb8c0]';

export default component$(() => {
  const state = useStore({ menuOpen: false, claimed: [] as string[], toast: '' });

  useVisibleTask$(({ cleanup }) => {
    let cancelled = false;
    import('motion').then(({ animate, stagger }) => {
      if (cancelled) return;
      animate('.motion-reveal', { opacity: [0, 1], y: [16, 0] }, { duration: 0.55, delay: stagger(0.06), ease: 'easeOut' });
      animate('[data-motion="bar"]', { scaleX: [0, 1] }, { duration: 0.9, ease: 'easeOut' });
    });
    cleanup(() => { cancelled = true; });
  });

  return (
    <div class="flex min-h-screen">
      <aside class={`fixed inset-y-0 left-0 z-10 w-[248px] border-r border-line bg-night/80 px-[18px] py-7 transition-transform duration-300 max-[650px]:w-[245px] max-[650px]:-translate-x-full max-[650px]:bg-night ${state.menuOpen ? 'max-[650px]:translate-x-0' : ''}`}>
        <div class="flex items-center gap-2.5 px-3 pb-10 text-xl font-extrabold tracking-[-.04em]"><span class="grid h-7 w-7 rotate-[-8deg] place-items-center rounded-[9px] bg-lime text-[17px] text-night">↗</span> contribo</div>
        <div class="px-3 pb-2.5 text-[11px] font-bold uppercase tracking-[.14em] text-[#59636e]">Workspace</div>
        <nav class="grid gap-1.5">
          {['◈ Quest board', '◒ My progression', '◇ Developer passport', '✓ Reviews'].map((label, index) => <button class={`flex w-full items-center gap-3 rounded-[10px] border-0 px-3 py-2.5 text-left text-muted transition hover:bg-[#1a2026] hover:text-ink ${index === 0 ? 'bg-[#1a2026] text-ink shadow-[inset_3px_0_#d8ff3e]' : ''}`} key={label} onClick$={() => { state.menuOpen = false; }}><span class="w-[18px] text-center text-base">{label.slice(0, 1)}</span>{label.slice(2)}{index === 3 && <span class="ml-auto text-[11px] text-lime">3</span>}</button>)}
        </nav>
        <div class="mt-8 px-3 pb-2.5 text-[11px] font-bold uppercase tracking-[.14em] text-[#59636e]">Explore</div>
        <nav class="grid gap-1.5">
          {['⌕ Repositories', '◎ Leaderboard', '♧ Community'].map((label) => <button class="flex w-full items-center gap-3 rounded-[10px] border-0 px-3 py-2.5 text-left text-muted transition hover:bg-[#1a2026] hover:text-ink" key={label}><span class="w-[18px] text-center text-base">{label.slice(0, 1)}</span>{label.slice(2)}</button>)}
        </nav>
        <div class="absolute bottom-[26px] left-[18px] right-[18px] rounded-[14px] border border-[#34421f] bg-gradient-to-br from-[#1a2316] to-[#121a15] p-4 max-[1000px]:hidden"><div class="text-[11px] font-extrabold uppercase tracking-[.12em] text-lime">Next unlock</div><p class="my-1.5 mb-3 text-xs text-[#b6c197]">Reach Contributor III to access advanced quests and review applications.</p><button class="border-0 bg-transparent p-0 text-xs font-bold text-lime">View progression →</button></div>
      </aside>

      <main class="ml-[248px] w-[calc(100%-248px)] max-w-[1500px] px-[4.5vw] pb-[60px] pt-7 max-[1000px]:ml-[205px] max-[1000px]:w-[calc(100%-205px)] max-[1000px]:px-[26px] max-[650px]:ml-0 max-[650px]:w-full max-[650px]:px-[15px] max-[650px]:pb-10 max-[650px]:pt-[18px]">
        <header class="mb-[34px] flex items-center justify-between max-[650px]:mb-[30px]"><div class="flex items-center text-[13px] text-muted"><button class="mr-2.5 hidden border-0 bg-transparent text-[21px] max-[650px]:inline-block" aria-label="Open navigation" onClick$={() => { state.menuOpen = !state.menuOpen; }}>☰</button><span>Workspace&nbsp; / &nbsp;</span><strong class="font-semibold text-ink">Quest board</strong></div><div class="flex items-center gap-5"><button class="relative border-0 bg-transparent text-lg text-muted" aria-label="Notifications">♧<i class="absolute right-[-3px] top-[-2px] h-1.5 w-1.5 rounded-full bg-lime"></i></button><div class="flex items-center gap-2.5 border-l border-line pl-[18px] max-[650px]:pl-3"><div class="grid h-[34px] w-[34px] place-items-center rounded-full bg-[#e2a77b] text-xs font-extrabold text-[#2a1711]">AK</div><div class="max-[650px]:hidden"><div class="text-[13px] font-bold">Alex Kim</div><div class="text-[11px] text-lime">Contributor II</div></div><span class="text-[#68737e]">⌄</span></div></div></header>
        <section class="motion-reveal mb-7 flex items-end justify-between gap-7 max-[650px]:block"><div><h1 class="mb-2.5 max-w-[620px] text-[clamp(26px,3vw,39px)] font-bold leading-[1.05] tracking-[-.055em]">Good morning, Alex.<br /><span class="text-lime">Your next contribution</span> is waiting.</h1><p class="m-0 max-w-[570px] text-muted">Three quests matched to your current skills, recent reviews, and a nudge beyond your comfort zone.</p></div><div class="whitespace-nowrap text-[13px] text-[#697580] max-[650px]:mt-3.5">Tuesday, September 19, 2026</div></section>
        <section class="mb-6 grid grid-cols-4 gap-3 max-[1000px]:grid-cols-2 max-[650px]:gap-2">
          {[['Total XP', '2,840', '+420 this month'], ['Merged contributions', '18', '+3 since last month'], ['Repositories', '9', 'Across 4 languages'], ['Upstream acceptance', '88%', '+6% all time']].map(([label, value, detail]) => <div class="motion-reveal rounded-xl border border-line bg-surface px-[18px] py-[17px] max-[650px]:p-3.5" key={label}><div class="mb-2 text-xs text-muted">{label}</div><div class="text-[25px] font-bold tracking-[-.04em] max-[650px]:text-[21px]">{value}</div><div class="mt-1 text-[11px] text-[#6f7b86]"><span class="text-lime">{detail.split(' ')[0]}</span>{detail.slice(detail.indexOf(' '))}</div></div>)}
        </section>
        <div class="grid grid-cols-[minmax(0,1.65fr)_minmax(290px,.82fr)] items-start gap-[18px] max-[1000px]:grid-cols-1">
          <section class="motion-reveal overflow-hidden rounded-[15px] border border-line bg-gradient-to-br from-[#181d23f0] to-[#0f1216f5] shadow-[0_18px_50px_rgba(0,0,0,.28)]"><div class="flex items-center justify-between px-[21px] pb-[15px] pt-5"><div><h2 class="m-0 text-base font-semibold tracking-[-.025em]">Recommended quests</h2><p class="m-0 mt-1 text-xs text-muted">Matched to your skills · refreshed just now</p></div><button class="border-0 bg-transparent text-xs font-bold text-lime" onClick$={() => { state.toast = 'All quests are already tuned to your current level.'; }}>View all →</button></div><div class="px-3 pb-3">
            {quests.map((quest) => {
              const claimed = state.claimed.includes(quest.title);
              return <article class="group grid grid-cols-[42px_minmax(0,1fr)_auto] items-center gap-[13px] border-t border-[#252c33] px-2 py-[17px] transition hover:rounded-[10px] hover:bg-[#1a2026] max-[650px]:grid-cols-[34px_minmax(0,1fr)]" key={quest.title}><div class={`grid h-10 w-10 place-items-center rounded-[10px] border text-base font-extrabold max-[650px]:h-[34px] max-[650px]:w-[34px] max-[650px]:text-[13px] ${quest.logoStyle}`}>{quest.icon}</div><div><h3 class="m-0 mb-1 text-sm font-semibold tracking-[-.015em]">{quest.title}</h3><div class="text-xs text-muted">{quest.repo}</div><div class="mt-2 flex flex-wrap gap-1.5">{quest.tags.map((tag) => <span class={`rounded px-1.5 py-0.5 text-[10px] ${tagStyle(tag)}`} key={tag}>{tag}</span>)}</div></div><div class="min-w-[92px] text-right max-[650px]:col-start-2 max-[650px]:flex max-[650px]:items-center max-[650px]:justify-between max-[650px]:text-left"><div><div class={`text-xs font-bold ${quest.difficultyStyle}`}>● {quest.difficulty} · {quest.level}</div><div class="mt-1 text-[11px] text-[#75818c] max-[650px]:hidden">{quest.time} · {quest.xp}</div></div><button class={`mt-2 rounded-md border-0 bg-lime px-2.5 py-1.5 text-[11px] font-extrabold text-[#12170c] transition hover:brightness-110 max-[650px]:mt-0 ${claimed ? 'bg-[#29351e] text-lime' : ''}`} disabled={claimed} onClick$={() => { state.claimed = [...state.claimed, quest.title]; state.toast = `“${quest.title}” added to your active quests.`; }}>{claimed ? 'Claimed' : 'Claim quest'}</button></div></article>;
            })}
          </div></section>
          <aside>
            <section class="motion-reveal mb-[18px] rounded-[15px] border border-line bg-gradient-to-br from-[#181d23f0] to-[#0f1216f5] p-[21px] shadow-[0_18px_50px_rgba(0,0,0,.28)]"><div class="flex items-start justify-between"><div><div class="text-[11px] font-extrabold uppercase tracking-[.12em] text-lime">Current rank</div><div class="my-1 text-[28px] font-extrabold tracking-[-.05em]">Contributor II</div></div><div class="text-[25px]">✦</div></div><div class="text-xs text-muted">2,840 / 4,100 XP to Contributor III</div><div class="my-4 h-1.5 overflow-hidden rounded bg-[#2b333a]"><span data-motion="bar" class="block h-full w-[69%] origin-left rounded bg-lime"></span></div><div class="flex justify-between text-[11px] text-[#71808a]"><span>69% complete</span><span>1,260 XP to go</span></div><div class="mt-[17px] border-t border-line pt-[15px] text-xs leading-relaxed text-[#aeb8bf]"><b class="text-ink">One stretch quest</b> and two merged PRs will put your next rank within reach.</div></section>
            <section class="motion-reveal rounded-[15px] border border-line bg-gradient-to-br from-[#181d23f0] to-[#0f1216f5] p-5 shadow-[0_18px_50px_rgba(0,0,0,.28)]"><div class="text-base font-semibold tracking-[-.025em]">Demonstrated skills</div><p class="m-0 mt-1 text-xs text-muted">Based on accepted contributions</p>{[['TypeScript','86 · Advanced','86'], ['Python','74 · Advanced','74'], ['Testing','68 · Proficient','68'], ['PostgreSQL','51 · Developing','51']].map(([name, rating, width]) => <div class="mt-4" key={name}><div class="mb-1.5 flex justify-between text-xs"><span>{name}</span><span class="text-muted">{rating}</span></div><div class="h-1 rounded bg-[#2b333a]"><i class="block h-full rounded bg-cyan" style={{ width: `${width}%` }}></i></div></div>)}</section>
            <section class="motion-reveal mt-[18px] rounded-[15px] border border-line bg-gradient-to-br from-[#181d23f0] to-[#0f1216f5] p-5 shadow-[0_18px_50px_rgba(0,0,0,.28)]"><div class="text-base font-semibold tracking-[-.025em]">Recent proof</div><div class="mt-4 flex gap-3 border-t border-line pt-4"><i class="mt-1.5 h-2 w-2 rounded-full bg-lime shadow-[0_0_0_4px_#29351e]"></i><div><p class="m-0 text-xs">PR #1832 was merged in <span class="text-cyan">cal.com</span></p><span class="text-[11px] text-muted">2 days ago · +320 XP</span></div></div><div class="mt-4 flex gap-3 border-t border-line pt-4"><i class="mt-1.5 h-2 w-2 rounded-full bg-cyan shadow-[0_0_0_4px_#1a303a]"></i><div><p class="m-0 text-xs">Review approved by <span class="text-cyan">Maya R.</span></p><span class="text-[11px] text-muted">5 days ago · Reviewer rep +80</span></div></div></section>
          </aside>
        </div>
      </main>
      {state.toast && <button class="fixed bottom-6 right-6 z-20 rounded-[10px] border-0 bg-[#e7ffc2] px-4 py-3 text-[13px] font-bold text-[#17200e] shadow-[0_18px_50px_rgba(0,0,0,.28)]" onClick$={() => { state.toast = ''; }}>{state.toast}</button>}
    </div>
  );
});

export const head: DocumentHead = { title: 'Contribo — Your code is your résumé', meta: [{ name: 'description', content: 'Earn your reputation in public through real open-source contributions.' }] };
