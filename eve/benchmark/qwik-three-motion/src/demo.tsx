import { component$, useSignal, useVisibleTask$ } from '@builder.io/qwik';

export const Demo = component$(() => {
  const canvas = useSignal<HTMLCanvasElement>();
  const energy = useSignal(0.62);
  const connected = useSignal(false);
  const events = useSignal<string[]>(['Qwik shell resumed', 'Waiting for a signal…']);
  const pulse = useSignal(0);

  useVisibleTask$(({ cleanup }) => {
    let renderer: any; let frame = 0; let stopMotion: any; let stopPulse: any; let scene: any; let camera: any; let field: any; let ring: any; let targetX = 0; let targetY = 0;
    const boot = async () => {
      const THREE = await import('three');
      const { animate } = await import('motion');
      if (!canvas.value) return;
      scene = new THREE.Scene(); camera = new THREE.PerspectiveCamera(42, innerWidth / innerHeight, 0.1, 100); camera.position.set(0, 1.8, 7.5);
      renderer = new THREE.WebGLRenderer({ canvas: canvas.value, alpha: true, antialias: true }); renderer.setPixelRatio(Math.min(devicePixelRatio, 2)); renderer.setSize(innerWidth, innerHeight);
      field = new THREE.Group(); scene.add(field);
      const geometry = new THREE.IcosahedronGeometry(1.6, 2); const material = new THREE.MeshPhysicalMaterial({ color: 0x74f5cc, emissive: 0x163f48, roughness: 0.26, metalness: 0.2, wireframe: true }); const core = new THREE.Mesh(geometry, material); field.add(core);
      const dots = new THREE.BufferGeometry(); const points = new Float32Array(360 * 3);
      for (let i = 0; i < 360; i++) { const r = 2.1 + Math.random() * 1.6; const a = Math.random() * Math.PI * 2; points[i * 3] = Math.cos(a) * r; points[i * 3 + 1] = (Math.random() - 0.5) * 3.4; points[i * 3 + 2] = Math.sin(a) * r; }
      dots.setAttribute('position', new THREE.BufferAttribute(points, 3)); field.add(new THREE.Points(dots, new THREE.PointsMaterial({ color: 0xa5fff0, size: 0.025, transparent: true, opacity: 0.7 })));
      ring = new THREE.Mesh(new THREE.TorusGeometry(2.25, 0.018, 12, 120), new THREE.MeshBasicMaterial({ color: 0xffc870, transparent: true, opacity: 0.8 })); ring.rotation.x = Math.PI / 2.3; field.add(ring); scene.add(new THREE.AmbientLight(0x9deee4, 2));
      const resize = () => { camera.aspect = innerWidth / innerHeight; camera.updateProjectionMatrix(); renderer.setSize(innerWidth, innerHeight); }; addEventListener('resize', resize);
      stopMotion = animate(field.rotation, { y: Math.PI * 2, z: -Math.PI * 2 }, { duration: 18, repeat: Infinity, ease: 'linear' });
      stopPulse = animate(core.scale, { x: 1.12, y: 1.12, z: 1.12 }, { duration: 1.8, repeat: Infinity, direction: 'alternate', ease: 'easeInOut' });
      const pointerMove = (event: PointerEvent) => { targetX = (event.clientX / innerWidth - 0.5) * 0.55; targetY = (event.clientY / innerHeight - 0.5) * 0.3; };
      addEventListener('pointermove', pointerMove);
      const draw = (time: number) => { frame = requestAnimationFrame(draw); const boost = energy.value; field.position.x += (targetX - field.position.x) * 0.025; field.position.y += (-targetY - field.position.y) * 0.025; core.rotation.x = time * 0.00012 * boost; core.rotation.z = time * 0.00008; ring.rotation.z = time * 0.0002 * boost; ring.rotation.y = Math.sin(time * 0.0007) * 0.28; renderer.render(scene, camera); }; frame = requestAnimationFrame(draw);
      cleanup(() => { cancelAnimationFrame(frame); removeEventListener('resize', resize); removeEventListener('pointermove', pointerMove); stopMotion?.stop?.(); stopPulse?.stop?.(); renderer?.dispose(); geometry.dispose(); dots.dispose(); });
    }; boot();
  });
  const addEvent = (message: string) => { events.value = [message, ...events.value].slice(0, 4); };
  const triggerPulse = () => { pulse.value++; connected.value = !connected.value; addEvent(connected.value ? 'Motion spring connected' : 'Signal paused'); };
  return <main>
    <canvas ref={canvas} class="scene" aria-label="Animated Three.js signal field" />
    <nav><div class="brand"><span>Q</span> QWIK<span class="dim">/LAB</span></div><button class="status" onClick$={() => triggerPulse()}><i class={{ on: connected.value }} />{connected.value ? 'LIVE SIGNAL' : 'CONNECT'}</button></nav>
    <section class="hero"><p class="eyebrow">A TEMPORARY EXPERIMENT · 01</p><h1>Resumable UI.<br /><em>Living geometry.</em></h1><p class="intro">Qwik wakes only the interaction you touch. Three.js turns data into a spatial signal. Motion makes the whole system feel alive.</p><button class="launch" onClick$={() => triggerPulse()}>Trigger a signal <span>↗</span></button></section>
    <aside class="panel"><div class="panel-head"><span>FIELD CONTROL</span><b>{Math.round(energy.value * 100)}%</b></div><label>Energy<input type="range" min="0.1" max="1" step="0.01" bind:value={energy} /></label><div class="meter"><span style={{ width: `${energy.value * 100}%` }} /></div><div class="chips"><span>QWIK <small>resumable</small></span><span>THREE <small>WebGL</small></span><span>MOTION <small>spring</small></span></div></aside>
    <section class="events"><div class="eyebrow">EVENT STREAM</div>{events.value.map((event, index) => <div class="event" key={event + index}><i />{event}<small>{index === 0 ? 'now' : `${index}s ago`}</small></div>)}</section>
    <footer><span>Qwik × Three.js × Motion</span><span>Scroll to explore the field ↓</span></footer>
  </main>;
});
