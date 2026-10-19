import { createApp } from 'vue';
import { ElButton, ElInput, ElInputNumber, ElTag, ElTable, ElTableColumn, ElDialog, ElLoading } from 'element-plus';
import 'element-plus/dist/index.css';
import './style.css';
import App from './App.vue';

const app = createApp(App);
for (const [name, component] of Object.entries({
  ElButton,
  ElInput,
  ElInputNumber,
  ElTag,
  ElTable,
  ElTableColumn,
  ElDialog,
}))
  app.component(name, component);
app.directive('loading', ElLoading.directive);
app.mount('#app');
