// TSK-001 scaffold entry: renders the app root. Real shell lands with TSK-202.
import { render } from 'solid-js/web';
import './app.css';
import { AppRouter } from './router';

const appRoot = document.getElementById('app');

if (!appRoot) {
  throw new Error('#app root element missing in index.html');
}

render(() => <AppRouter />, appRoot);
