import { createApp } from 'vue'
import { createPinia } from 'pinia'
import View from '../src/views/admin/DoneBotTemplatesView.vue'
import i18n from '../src/i18n'
import '../src/style.css'

createApp(View).use(createPinia()).use(i18n).mount('#app')
