import { component$ } from '@builder.io/qwik';
import { QwikCityProvider, RouterOutlet, ServiceWorkerRegister } from '@builder.io/qwik-city';
import './global.css';

export default component$(() => (
  <QwikCityProvider>
    <head>
      <meta charSet="utf-8" />
      <meta name="viewport" content="width=device-width, initial-scale=1" />
      <meta name="theme-color" content="#0c0e11" />
    </head>
    <body>
      <RouterOutlet />
      <ServiceWorkerRegister />
    </body>
  </QwikCityProvider>
));
