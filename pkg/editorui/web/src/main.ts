import { mount } from 'svelte';
import './lib/styles/app.css';
import App from './App.svelte';
import { ui } from './lib/stores/ui.svelte';

ui.init();

const target = document.getElementById('app');
if (!target) throw new Error('#app mount target not found');

const app = mount(App, { target });

export default app;
