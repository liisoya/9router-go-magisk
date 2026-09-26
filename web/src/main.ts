import 'material-symbols/outlined.css'
import { mount } from 'svelte'
import App from './App.svelte'
import './index.css'
import { registerServiceWorker } from './lib/pwa'

registerServiceWorker()

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
