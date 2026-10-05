// Browser entry point for the shared Hermes data-plane runtime.
// The Node benchmark and the browser use the same implementation and API.
// Hermes is authored as CommonJS for the Node runtime. Its browser-safe global
// bridge lets Vite load the same implementation without a second copy.
import '../../Hermes/Hermes.js';

const hermesModule = globalThis.__HERMES_RUNTIME__;

export const { Hermes, HermesWorld, TypedTable } = hermesModule;
export const Phase = Hermes.Phase;
