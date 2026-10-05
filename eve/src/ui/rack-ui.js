const $ = (id) => document.getElementById(id);
function node(tag, content, className) {
  const n = document.createElement(tag);
  n.textContent = content;
  if (className) n.className = className;
  return n;
}
export function installAdamUI() {
  $('search').placeholder = 'Pod, location, rack or component serial…';
  $('tourSource').textContent = 'ADAM observations · schematic geometry';
  document.querySelector('.scale span').textContent =
    '6 ft reference · layout/dimensions unverified';
  document.querySelector('#inspector dl').remove();
  $('approach').insertAdjacentHTML(
    'beforebegin',
    '<div id="rackFacts"></div><div id="componentPanel"><h3>Component inventory</h3><p class="muted">Schematic slots · not physical rack-unit placement</p><input id="componentSearch" aria-label="Search components" placeholder="Slot, serial, type or stage"><label class="component-filter"><input id="failuresOnly" type="checkbox"> Failures only</label><div id="components"></div></div>',
  );
  document
    .querySelector('.world-toolbar')
    .insertAdjacentHTML(
      'afterend',
      '<div class="state-legend"><span>● Failed</span><span>● Running</span><span>● Passed</span><span>● Unknown</span><span>□ Empty location</span></div>',
    );
}
let currentId = null;
export function renderRackDetails(a, { historical, feedState }) {
  if (currentId !== a.id) {
    $('componentSearch').value = '';
    $('failuresOnly').checked = false;
    currentId = a.id;
  }
  const facts = $('rackFacts');
  facts.replaceChildren();
  const rows = [
    ['Hierarchy', `Building ${a.building} / Pod ${a.pod} / ${a.location}`],
    ['Source', historical ? 'Historical ADAM snapshot' : `ADAM · ${feedState}`],
    ['Geometry', 'Approximate · dimensions unverified'],
  ];
  if (a.type === 'rack')
    rows.push(
      [
        'Equipment',
        a.baseOnly
          ? 'Base rack envelope · no ADAM observation at this location'
          : a.kind === 'L10'
            ? 'L10 test station · no rack serial supplied'
            : `L11 rack · ${a.serial}`,
      ],
      ['Installed / empty slots', `${a.summary.installed} / ${a.summary.empty}`],
      [
        'Results',
        `${a.summary.failed} failed · ${a.summary.running} running · ${a.summary.passed} passed · ${a.summary.unknown} unknown`,
      ],
      [
        'Cable observations',
        a.cables?.length
          ? `${a.cables.length} ADAM message${a.cables.length === 1 ? '' : 's'}`
          : 'None reported',
      ],
      ['Test completion', 'Unavailable · model test weights required'],
      ['Check-in (source)', a.checkin || 'Unknown'],
      ['Collected (source)', a.sourceTime || 'Unknown'],
      ['Timezone', a.timestamp == null ? 'Unspecified by source' : 'Explicit source offset'],
    );
  const dl = document.createElement('dl');
  for (const [key, value] of rows) {
    const row = document.createElement('div');
    row.append(node('dt', key), node('dd', value));
    dl.append(row);
  }
  facts.append(dl);
  if (a.type === 'rack' && a.cables?.length) {
    const cableBlock = document.createElement('div');
    cableBlock.className = 'cable-observations';
    cableBlock.append(node('h3', 'Cable observations'));
    cableBlock.append(
      node(
        'p',
        'Rendered connections are evidence-based visual interpretations; ADAM does not provide measured cable topology.',
        'muted',
      ),
    );
    for (const c of a.cables)
      cableBlock.append(node('p', `${c.type} · ${c.side} · ${c.state}: ${c.note}`));
    facts.append(cableBlock);
  }
  $('componentPanel').hidden = a.type !== 'rack';
  const openSlots = new Set(
    [...$('components').querySelectorAll('details[open]')].map((n) => n.dataset.slot),
  );
  function draw() {
    const query = $('componentSearch').value.toLowerCase();
    $('components').replaceChildren();
    const matches = a.components.filter(
      (c) =>
        (!$('failuresOnly').checked || c.state === 'failed') &&
        `${c.slot} ${c.serial} ${c.type} ${c.stage} ${c.result}`.toLowerCase().includes(query),
    );
    for (const c of matches) {
      const detail = document.createElement('details');
      detail.dataset.slot = c.slot;
      detail.open = openSlots.has(c.slot);
      detail.append(
        node('summary', `${c.slot} · ${c.type} · ${c.type === 'SPACE' ? 'empty' : c.state}`),
      );
      const lines = [
        ['Serial', c.serial],
        ['Part', c.part],
        ['Stage', c.stage],
        ['Station', c.station],
        ['Result', c.result],
        ['Status', c.status],
        ['Last event (source)', c.eventTime],
      ];
      for (const [key, value] of lines)
        detail.append(node('p', `${key}: ${value || 'Not reported'}`));
      detail.append(node('h4', 'Recorded stage results'));
      for (const r of c.results)
        detail.append(
          node(
            'p',
            `${r.stage} · ${r.result || 'Unknown'} · ${r.station || 'No station'} · ${r.time || 'No timestamp'}`,
          ),
        );
      if (!c.results.length) detail.append(node('p', 'No recorded stage results.'));
      $('components').append(detail);
    }
    if (!matches.length) $('components').append(node('p', 'No matching components.', 'muted'));
  }
  $('componentSearch').oninput = draw;
  $('failuresOnly').onchange = draw;
  draw();
}
