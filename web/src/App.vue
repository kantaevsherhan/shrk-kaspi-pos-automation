<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue';
import QRCode from 'qrcode';
import { ElMessage, ElMessageBox } from 'element-plus';
import {
  LayoutDashboard,
  FileText,
  QrCode,
  History,
  RotateCcw,
  Settings2,
  LogOut,
  Plus,
  ArrowUpRight,
  RefreshCw,
  ShieldCheck,
  Wallet,
  Search,
  Copy,
  CheckCircle2,
  AlertCircle,
  Link2,
  Send,
  ChevronRight,
  Menu,
  X,
  Clock3,
  Smartphone,
  KeyRound,
} from '@lucide/vue';

const adminConfigured = ref(true);
const username = ref('');
const password = ref('');
const setupKey = ref('');
const unlocked = ref(false);
const connected = ref(false);
const account = ref({});
const loading = ref(false);
const busy = ref(false);
const page = ref('overview');
const mobileMenu = ref(false);
const processId = ref('');
const authStep = ref(0);
const phone = ref('');
const otp = ref('');
const invoice = ref({ phoneNumber: '', amount: null, comment: '' });
const qrAmount = ref(null);
const refund = ref({ qrOperationId: '', returnAmount: null });
const createdInvoice = ref(null);
const createdQR = ref(null);
const qrImage = ref('');
const qrStatus = ref('');
const invoiceRows = ref([]);
const operationRows = ref([]);
const salesStats = ref({});
const activePayments = ref([]);
const selectedRow = ref(null);
const selectedDetail = ref(null);
const search = ref('');
const lastRefresh = ref(null);
const checkResult = ref('');
const monitorInfo = ref({ telegramConfigured: false, status: 'unknown', lastChecked: null });
let qrTimer;

const nav = [
  { id: 'overview', label: 'Обзор', icon: LayoutDashboard },
  { id: 'invoices', label: 'Счета', icon: FileText },
  { id: 'qr', label: 'QR-оплата', icon: QrCode },
  { id: 'history', label: 'История', icon: History },
  { id: 'refund', label: 'Возвраты', icon: RotateCcw },
  { id: 'settings', label: 'Подключение', icon: Settings2 },
];
const current = computed(() => nav.find((x) => x.id === page.value));
const money = (value) => new Intl.NumberFormat('ru-KZ', { maximumFractionDigits: 2 }).format(Number(value || 0)) + ' ₸';
const statusText = (status) =>
  ({
    Processed: 'Оплачено',
    RemotePaymentCreated: 'Ожидает оплаты',
    QrTokenCreated: 'Ожидает сканирования',
    QrTokenScanned: 'QR отсканирован',
    PaymentConfirmation: 'Подтверждение',
    RemotePaymentCanceled: 'Отменено',
    RemotePaymentRejected: 'Отклонено',
    Expired: 'Истекло',
    QrTokenDiscarded: 'Истекло',
  })[status] ||
  status ||
  'Неизвестно';
const statusType = (status) =>
  status === 'Processed'
    ? 'success'
    : ['Expired', 'QrTokenDiscarded', 'RemotePaymentCanceled', 'RemotePaymentRejected', 'Rejected'].includes(status)
      ? 'danger'
      : 'warning';
const allRows = computed(() => [
  ...invoiceRows.value.map((x) => ({ ...x, kind: 'Счёт' })),
  ...operationRows.value.map((x) => ({ ...x, kind: 'Операция' })),
]);
const filteredRows = computed(() =>
  allRows.value.filter((x) => !search.value || JSON.stringify(x).toLowerCase().includes(search.value.toLowerCase())),
);
const paidCount = computed(() => Number(salesStats.value.SalesCount || 0));
const paidSum = computed(() => Number(salesStats.value.SalesAmount || 0));
const kaspiEndDate = () => {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Almaty',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(new Date());
  const value = Object.fromEntries(parts.map((x) => [x.type, x.value]));
  return `${value.year}-${value.month}-${value.day}T23:59:59.000+0500`;
};

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    credentials: 'same-origin',
    headers: {
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(options.headers || {}),
    },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    if (response.status === 401 && data.error === 'Unauthorized') {
      unlocked.value = false;
      connected.value = false;
    }
    throw new Error(data.error || `HTTP ${response.status}`);
  }
  return data;
}
const post = (path, data = {}) => api(path, { method: 'POST', body: JSON.stringify(data) });
function kaspiData(data) {
  if (data.StatusCode !== undefined && Number(data.StatusCode) !== 0)
    throw new Error(data.Message || data.StatusDesc || `Kaspi: ${data.StatusCode}`);
  return data.Data || {};
}
function report(error) {
  ElMessage.error(error?.message || 'Не удалось выполнить запрос');
}
function getRows(data) {
  const root = data.Data || data;
  if (Array.isArray(root)) return root;
  if (Array.isArray(root?.DailySets))
    return root.DailySets.flatMap((day) => (day.Operations || []).map((op) => ({ ...op, Day: day.Date })));
  for (const key of ['Items', 'Operations', 'History', 'Payments', 'Transactions', 'Result', 'Data'])
    if (Array.isArray(root?.[key])) return root[key];
  return [];
}

async function unlock() {
  if (!username.value.trim() || !password.value) return;
  busy.value = true;
  try {
    if (!adminConfigured.value) {
      await api('/api/admin/setup', {
        method: 'POST',
        headers: { Authorization: `Bearer ${setupKey.value.trim()}` },
        body: JSON.stringify({ username: username.value.trim(), password: password.value }),
      });
      adminConfigured.value = true;
      setupKey.value = '';
    }
    await post('/api/admin/login', { username: username.value.trim(), password: password.value });
    password.value = '';
    unlocked.value = true;
    await loadSession();
    await loadMonitor();
    if (connected.value) await refreshAll();
  } catch (error) {
    report(error);
  } finally {
    busy.value = false;
  }
}
async function lock() {
  try {
    await post('/api/admin/logout');
  } catch {
    /* Clear the local view even if the session expired. */
  }
  unlocked.value = false;
  connected.value = false;
  page.value = 'overview';
}
async function loadSession() {
  const result = await api('/api/auth/session');
  connected.value = result.authenticated;
  account.value = result;
}
async function loadMonitor() {
  monitorInfo.value = await api('/api/admin/monitor');
}
async function testTelegram() {
  busy.value = true;
  try {
    await post('/api/admin/notifications/test');
    ElMessage.success('Тестовое сообщение отправлено');
  } catch (error) {
    report(error);
  } finally {
    busy.value = false;
  }
}
async function refreshAll() {
  if (!connected.value) return;
  loading.value = true;
  const results = await Promise.allSettled([
    post('/api/invoice/history'),
    post('/api/history/operations', { endDate: kaspiEndDate(), lastTransactionDate: '', statementPeriodCode: 0 }),
    api('/api/payments/active'),
  ]);
  if (results[0].status === 'fulfilled') invoiceRows.value = getRows(results[0].value);
  if (results[1].status === 'fulfilled') {
    operationRows.value = getRows(results[1].value);
    salesStats.value = results[1].value.Data?.Statistic || {};
  }
  if (results[2].status === 'fulfilled') activePayments.value = results[2].value.items || [];
  if (results.every((x) => x.status === 'rejected')) report(results[0].reason);
  lastRefresh.value = new Date();
  loading.value = false;
}
function changePage(id) {
  page.value = id;
  mobileMenu.value = false;
  if (id === 'history' || id === 'overview') refreshAll();
  if (id === 'settings') loadMonitor().catch(report);
}
async function createInvoice() {
  if (!/^\d{10,11}$/.test(invoice.value.phoneNumber) || Number(invoice.value.amount) <= 0) {
    ElMessage.warning('Введите телефон и сумму');
    return;
  }
  busy.value = true;
  try {
    const result = await post('/api/invoice/create', invoice.value);
    createdInvoice.value = kaspiData(result);
    ElMessage.success('Счёт создан');
    await refreshAll();
  } catch (error) {
    report(error);
  } finally {
    busy.value = false;
  }
}
async function createQR() {
  if (Number(qrAmount.value) <= 0) {
    ElMessage.warning('Введите сумму');
    return;
  }
  busy.value = true;
  try {
    const result = await post('/api/qr/create', { amount: Number(qrAmount.value) });
    createdQR.value = kaspiData(result);
    if (!createdQR.value.QrOriginalToken) throw new Error('Kaspi не вернул QR-токен');
    qrStatus.value = createdQR.value.Status || 'QrTokenCreated';
    qrImage.value = await QRCode.toDataURL(createdQR.value.QrOriginalToken, {
      width: 320,
      margin: 1,
      color: { dark: '#171b26', light: '#ffffff' },
    });
    ElMessage.success('QR создан');
    startQRStatus();
    await refreshAll();
  } catch (error) {
    report(error);
  } finally {
    busy.value = false;
  }
}
function startQRStatus() {
  clearInterval(qrTimer);
  if (!createdQR.value?.QrOperationId) return;
  qrTimer = setInterval(async () => {
    try {
      const result = await api(`/api/qr/status?qrOperationId=${encodeURIComponent(createdQR.value.QrOperationId)}`);
      const next = result.Data?.Status;
      if (next) qrStatus.value = next;
      if (['Processed', 'Expired', 'QrTokenDiscarded', 'Rejected', 'CancelledByUser'].includes(next)) {
        clearInterval(qrTimer);
        refreshAll();
      }
    } catch {
      /* Polling remains available on the server. */
    }
  }, 3000);
}
async function createRefund() {
  if (!refund.value.qrOperationId || Number(refund.value.returnAmount) <= 0) {
    ElMessage.warning('Введите ID операции и сумму');
    return;
  }
  try {
    await ElMessageBox.confirm(
      `Вернуть ${money(refund.value.returnAmount)} по операции ${refund.value.qrOperationId}?`,
      'Подтвердить возврат',
      { type: 'warning', confirmButtonText: 'Вернуть', cancelButtonText: 'Отмена' },
    );
    busy.value = true;
    const result = await post('/api/refund/create', refund.value);
    kaspiData(result);
    ElMessage.success('Запрос на возврат отправлен');
    refund.value = { qrOperationId: '', returnAmount: null };
    await refreshAll();
  } catch (error) {
    if (error !== 'cancel') report(error);
  } finally {
    busy.value = false;
  }
}
async function cancelInvoice(row) {
  const id = row.QrOperationId || row.Id;
  try {
    await ElMessageBox.confirm(`Отменить счёт № ${id}?`, 'Подтвердить отмену', {
      type: 'warning',
      confirmButtonText: 'Отменить счёт',
      cancelButtonText: 'Назад',
    });
    const result = await post('/api/invoice/cancel', { operationId: String(id) });
    kaspiData(result);
    ElMessage.success('Счёт отменён');
    await refreshAll();
  } catch (error) {
    if (error !== 'cancel') report(error);
  }
}
async function checkSession() {
  busy.value = true;
  try {
    const result = await api('/api/session/check');
    checkResult.value = result.active ? 'Сессия Kaspi активна' : 'Сессия недоступна';
    ElMessage.success(checkResult.value);
  } catch (error) {
    checkResult.value = 'Проверка не пройдена';
    report(error);
  } finally {
    busy.value = false;
  }
}
async function logoutKaspi() {
  try {
    await ElMessageBox.confirm('Отключить аккаунт Kaspi от сервиса?', 'Отключение', {
      type: 'warning',
      confirmButtonText: 'Отключить',
      cancelButtonText: 'Отмена',
    });
    await post('/api/auth/logout');
    connected.value = false;
    account.value = {};
    authStep.value = 0;
    changePage('settings');
  } catch (error) {
    if (error !== 'cancel') report(error);
  }
}
async function beginAuth() {
  busy.value = true;
  try {
    const result = await post('/api/auth/init');
    if (!result.success) throw new Error('Kaspi не начал вход');
    processId.value = result.processId;
    authStep.value = 1;
  } catch (error) {
    report(error);
  } finally {
    busy.value = false;
  }
}
async function sendPhone() {
  busy.value = true;
  try {
    const result = await post('/api/auth/send-phone', { processId: processId.value, phoneNumber: phone.value });
    if (!result.success) throw new Error(result.errorMessage || result.desc || 'Kaspi не отправил SMS');
    authStep.value = 2;
    ElMessage.success('Код отправлен');
  } catch (error) {
    report(error);
  } finally {
    busy.value = false;
  }
}
async function verifyOTP() {
  busy.value = true;
  try {
    const result = await post('/api/auth/verify-otp', { processId: processId.value, otp: otp.value });
    if (!result.success) throw new Error('Код не принят Kaspi');
    await loadSession();
    authStep.value = 0;
    otp.value = '';
    ElMessage.success('Kaspi подключён');
    await refreshAll();
  } catch (error) {
    report(error);
  } finally {
    busy.value = false;
  }
}
async function copy(text) {
  try {
    await navigator.clipboard.writeText(String(text));
    ElMessage.success('Скопировано');
  } catch {
    ElMessage.error('Не удалось скопировать');
  }
}
async function openDetail(row) {
  selectedRow.value = row;
  selectedDetail.value = null;
  try {
    const result =
      row.kind === 'Счёт'
        ? await api(`/api/invoice/details?operationId=${encodeURIComponent(row.QrOperationId || row.Id)}`)
        : await post('/api/history/details', { id: String(row.Id), operationMethod: Number(row.OperationMethod || 0) });
    selectedDetail.value = kaspiData(result);
  } catch (error) {
    report(error);
  }
}
function refundFromDetail() {
  refund.value.qrOperationId = String(selectedRow.value.QrOperationId || selectedRow.value.Id || '');
  refund.value.returnAmount = Number(selectedRow.value.Amount || 0);
  selectedRow.value = null;
  changePage('refund');
}
onMounted(async () => {
  try {
    const status = await api('/api/admin/status');
    adminConfigured.value = status.configured;
    if (!status.configured) return;
    const me = await api('/api/admin/me');
    username.value = me.username;
    unlocked.value = true;
    await loadSession();
    await loadMonitor();
    if (connected.value) await refreshAll();
  } catch {
    /* A missing browser session returns the user to the login form. */
  }
});
onUnmounted(() => clearInterval(qrTimer));
</script>

<template>
  <div v-if="!unlocked" class="gate">
    <div class="gate-art">
      <div class="art-orbit one"></div>
      <div class="art-orbit two"></div>
      <div class="art-card">
        <div class="art-symbol">K<span>.</span></div>
        <div class="art-lines"><i></i><i></i><i></i></div>
      </div>
      <div class="art-caption">ЕДИНОЕ ПРОСТРАНСТВО ДЛЯ ОПЛАТ</div>
    </div>
    <div class="gate-form">
      <div class="brand">
        <span class="brand-mark">K<span>.</span></span
        ><span>Kaspi POS <b>Console</b></span>
      </div>
      <div class="gate-content">
        <span class="eyebrow">{{ adminConfigured ? 'БЕЗОПАСНЫЙ ВХОД' : 'ПЕРВАЯ НАСТРОЙКА' }}</span>
        <h1>Ваши платежи.<br /><em>Один экран.</em></h1>
        <p>
          {{
            adminConfigured
              ? 'Войдите в панель по имени и паролю.'
              : 'Создайте администратора. Сервисный API-ключ понадобится только один раз.'
          }}
        </p>
        <label v-if="!adminConfigured" class="field-label">Сервисный API-ключ</label>
        <el-input
          v-if="!adminConfigured"
          v-model="setupKey"
          type="password"
          show-password
          placeholder="Ключ из SERVICE_API_KEYS"
          size="large"
        />
        <label class="field-label">Имя пользователя</label>
        <el-input v-model="username" autocomplete="username" placeholder="Например, admin" size="large" />
        <label class="field-label">Пароль <small v-if="!adminConfigured">от 12 символов</small></label>
        <el-input
          v-model="password"
          type="password"
          show-password
          autocomplete="current-password"
          placeholder="Введите пароль"
          size="large"
          @keyup.enter="unlock"
          ><template #prefix><KeyRound :size="17" /></template></el-input
        ><el-button type="primary" size="large" class="wide" :loading="busy" @click="unlock"
          >{{ adminConfigured ? 'Войти' : 'Создать и войти' }} <ArrowUpRight :size="18"
        /></el-button>
        <div class="gate-note"><ShieldCheck :size="16" /> Сессия панели защищена сервером</div>
      </div>
      <div class="gate-footer">Kaspi POS Console · панель управления платежами</div>
    </div>
  </div>
  <div v-else class="shell">
    <div v-if="mobileMenu" class="mobile-scrim" @click="mobileMenu = false"></div>
    <aside class="sidebar" :class="{ open: mobileMenu }">
      <div class="side-head">
        <div class="brand">
          <span class="brand-mark">K<span>.</span></span
          ><span>Kaspi POS <b>Console</b></span>
        </div>
        <button class="mobile-close" @click="mobileMenu = false"><X :size="21" /></button>
      </div>
      <div class="side-section-label">РАБОЧЕЕ ПРОСТРАНСТВО</div>
      <nav>
        <button
          v-for="item in nav"
          :key="item.id"
          class="nav-item"
          :class="{ active: page === item.id }"
          @click="changePage(item.id)"
        >
          <component :is="item.icon" :size="19" stroke-width="1.9" /><span>{{ item.label }}</span
          ><ChevronRight v-if="page === item.id" :size="15" class="nav-chevron" />
        </button>
      </nav>
      <div class="side-bottom">
        <div class="connection-card">
          <div class="connection-icon"><Link2 :size="19" /></div>
          <div>
            <strong>Kaspi Pay</strong><small>{{ connected ? 'Аккаунт подключён' : 'Требуется подключение' }}</small>
          </div>
          <span class="live-dot" :class="{ off: !connected }"></span>
        </div>
        <button class="side-lock" @click="lock"><LogOut :size="18" /> Выйти из панели</button>
      </div>
    </aside>
    <div class="main-wrap">
      <header class="topbar">
        <div class="top-left">
          <button class="menu-button" @click="mobileMenu = true"><Menu :size="23" /></button
          ><span class="breadcrumbs"
            >Рабочее пространство <ChevronRight :size="14" /> <b>{{ current?.label }}</b></span
          >
        </div>
        <div class="top-right">
          <span class="top-date">{{
            new Intl.DateTimeFormat('ru-KZ', { day: 'numeric', month: 'long', year: 'numeric' }).format(new Date())
          }}</span>
          <div class="avatar">{{ (account.orgName || 'K').slice(0, 1).toUpperCase() }}</div>
        </div>
      </header>
      <main class="content">
        <template v-if="!connected && page !== 'settings'"
          ><div class="connect-prompt">
            <div class="prompt-icon"><Smartphone :size="30" /></div>
            <span class="eyebrow">ПОДКЛЮЧЕНИЕ</span>
            <h1>Подключите Kaspi Pay</h1>
            <p>Для создания счетов и QR-кодов сначала подтвердите вход через SMS.</p>
            <el-button type="primary" size="large" @click="changePage('settings')"
              >Перейти к подключению <ArrowUpRight :size="17"
            /></el-button></div
        ></template>
        <template v-else>
          <div class="page-heading">
            <div>
              <span class="eyebrow">KASPI POS CONSOLE</span>
              <h1>
                {{ page === 'overview' ? `Добрый день${account.orgName ? ', ' + account.orgName : ''}` : current?.label
                }}<span v-if="page === 'overview'" class="period"> 👋</span>
              </h1>
              <p>
                {{
                  {
                    overview: 'Всё важное по платежам в одном месте',
                    invoices: 'Создавайте счета и отслеживайте их оплату',
                    qr: 'Мгновенная оплата по QR-коду',
                    history: 'Операции напрямую из Kaspi',
                    refund: 'Оформление возвратов по операции',
                    settings: 'Управление подключением к Kaspi Pay',
                  }[page]
                }}
              </p>
            </div>
            <el-button
              v-if="['overview', 'history', 'invoices'].includes(page) && connected"
              plain
              :loading="loading"
              @click="refreshAll"
              ><RefreshCw :size="16" /> Обновить</el-button
            >
          </div>
          <template v-if="page === 'overview'"
            ><div class="stats">
              <div class="stat-card">
                <div class="stat-top">
                  <span>Продажи</span><span class="stat-icon mint"><Wallet :size="20" /></span>
                </div>
                <strong>{{ money(paidSum) }}</strong
                ><small>По сводке Kaspi за день</small>
              </div>
              <div class="stat-card">
                <div class="stat-top">
                  <span>Операций продажи</span><span class="stat-icon blue"><CheckCircle2 :size="20" /></span>
                </div>
                <strong>{{ paidCount }}</strong
                ><small>По сводке Kaspi за день</small>
              </div>
              <div class="stat-card">
                <div class="stat-top">
                  <span>Ожидают оплаты</span><span class="stat-icon amber"><Clock3 :size="20" /></span>
                </div>
                <strong>{{ activePayments.length }}</strong
                ><small>Активные платежи</small>
              </div>
            </div>
            <div class="section-grid">
              <section class="panel quick-panel">
                <div class="panel-header">
                  <div>
                    <span class="eyebrow">БЫСТРЫЕ ДЕЙСТВИЯ</span>
                    <h2>Начните принимать оплату</h2>
                  </div>
                </div>
                <div class="quick-actions">
                  <button @click="changePage('invoices')">
                    <span class="quick-icon coral"><FileText :size="23" /></span
                    ><span><strong>Выставить счёт</strong><small>Отправьте клиенту по номеру телефона</small></span
                    ><ArrowUpRight :size="18" /></button
                  ><button @click="changePage('qr')">
                    <span class="quick-icon violet"><QrCode :size="23" /></span
                    ><span><strong>Создать QR</strong><small>Для оплаты на месте</small></span
                    ><ArrowUpRight :size="18" />
                  </button>
                </div>
              </section>
              <section class="panel account-panel">
                <span class="eyebrow">ПОДКЛЮЧЕНИЕ</span>
                <div class="account-emblem">
                  <span>K<span>.</span></span>
                </div>
                <h2>{{ account.orgName || 'Kaspi Pay' }}</h2>
                <p>{{ account.phone || 'Аккаунт подключён' }}</p>
                <el-tag type="success" effect="light" round>● Активно</el-tag
                ><button class="text-link" @click="changePage('settings')">
                  Настройки подключения <ArrowUpRight :size="16" />
                </button>
              </section>
            </div>
            <section class="panel table-panel">
              <div class="panel-header">
                <div>
                  <span class="eyebrow">ПОСЛЕДНИЕ СОБЫТИЯ</span>
                  <h2>Недавние операции</h2>
                </div>
                <button class="text-link" @click="changePage('history')">
                  Вся история <ArrowUpRight :size="16" />
                </button>
              </div>
              <el-table
                :data="allRows.slice(0, 5)"
                v-loading="loading"
                empty-text="Операции пока не найдены"
                style="width: 100%"
                @row-click="openDetail"
                ><el-table-column label="Операция" min-width="150"
                  ><template #default="{ row }"
                    ><b>#{{ row.QrOperationId || row.Id || '—' }}</b
                    ><small class="row-sub">{{ row.kind }}</small></template
                  ></el-table-column
                ><el-table-column label="Сумма" min-width="120"
                  ><template #default="{ row }">{{ money(row.Amount || row.PaymentAmount) }}</template></el-table-column
                ><el-table-column label="Статус" min-width="150"
                  ><template #default="{ row }"
                    ><el-tag :type="statusType(row.Status)" effect="light" round>{{
                      statusText(row.Status)
                    }}</el-tag></template
                  ></el-table-column
                ></el-table
              >
            </section></template
          >
          <template v-if="page === 'invoices'"
            ><div class="work-grid">
              <section class="panel form-panel">
                <div class="panel-header">
                  <div>
                    <span class="eyebrow">НОВЫЙ СЧЁТ</span>
                    <h2>Выставить счёт клиенту</h2>
                  </div>
                  <span class="form-ornament"><FileText :size="22" /></span>
                </div>
                <div class="form-fields">
                  <label class="field-label">Номер телефона клиента</label
                  ><el-input v-model="invoice.phoneNumber" size="large" placeholder="7XXXXXXXXXX" maxlength="11" /><span
                    class="field-help"
                    >Номер в формате 7XXXXXXXXXX</span
                  ><label class="field-label">Сумма, ₸</label
                  ><el-input-number
                    v-model="invoice.amount"
                    :min="1"
                    :max="1000000000"
                    :precision="2"
                    :controls="false"
                    size="large"
                    placeholder="0"
                    class="wide"
                  /><label class="field-label">Комментарий <small>необязательно</small></label
                  ><el-input
                    v-model="invoice.comment"
                    size="large"
                    placeholder="Например, заказ № 123"
                    maxlength="200"
                  /><el-button type="primary" size="large" class="wide submit" :loading="busy" @click="createInvoice"
                    ><Send :size="17" /> Создать счёт</el-button
                  >
                </div>
              </section>
              <section class="panel result-panel">
                <template v-if="createdInvoice"
                  ><div class="result-success"><CheckCircle2 :size="28" /></div>
                  <span class="eyebrow">СЧЁТ СОЗДАН</span>
                  <h2>Ожидает оплаты</h2>
                  <p>Клиент получит счёт в Kaspi Pay.</p>
                  <div class="detail-line">
                    <span>ID операции</span><b>{{ createdInvoice.QrOperationId || createdInvoice.Id }}</b>
                  </div>
                  <div class="detail-line">
                    <span>Сумма</span><b>{{ money(invoice.amount) }}</b>
                  </div>
                  <el-button plain class="wide" @click="copy(createdInvoice.QrOperationId || createdInvoice.Id)"
                    ><Copy :size="16" /> Скопировать ID</el-button
                  ></template
                ><template v-else
                  ><div class="empty-art"><FileText :size="46" /></div>
                  <h2>Новый счёт появится здесь</h2>
                  <p>Укажите телефон и сумму, чтобы отправить клиенту запрос на оплату.</p></template
                >
              </section>
            </div>
            <section class="panel table-panel">
              <div class="panel-header">
                <div>
                  <span class="eyebrow">ИСТОРИЯ СЧЕТОВ</span>
                  <h2>Последние счета</h2>
                </div>
              </div>
              <el-table :data="invoiceRows" v-loading="loading" empty-text="Счета пока не найдены"
                ><el-table-column label="ID" min-width="120"
                  ><template #default="{ row }">#{{ row.QrOperationId || row.Id || '—' }}</template></el-table-column
                ><el-table-column label="Клиент" min-width="130"
                  ><template #default="{ row }">{{
                    row.ClientMobile || row.PhoneNumber || '—'
                  }}</template></el-table-column
                ><el-table-column label="Сумма" min-width="130"
                  ><template #default="{ row }">{{ money(row.Amount) }}</template></el-table-column
                ><el-table-column label="Статус" min-width="150"
                  ><template #default="{ row }"
                    ><el-tag :type="statusType(row.Status)" effect="light" round>{{
                      statusText(row.Status)
                    }}</el-tag></template
                  ></el-table-column
                ><el-table-column label="" width="110"
                  ><template #default="{ row }"
                    ><el-button
                      v-if="row.Status === 'RemotePaymentCreated'"
                      text
                      type="danger"
                      @click="cancelInvoice(row)"
                      >Отменить</el-button
                    ></template
                  ></el-table-column
                ></el-table
              >
            </section></template
          >
          <template v-if="page === 'qr'"
            ><div class="work-grid">
              <section class="panel form-panel">
                <div class="panel-header">
                  <div>
                    <span class="eyebrow">НОВАЯ ОПЛАТА</span>
                    <h2>Создать QR-код</h2>
                  </div>
                  <span class="form-ornament violet"><QrCode :size="22" /></span>
                </div>
                <p class="panel-intro">Покажите код покупателю. Оплату можно подтвердить прямо на этом экране.</p>
                <div class="form-fields">
                  <label class="field-label">Сумма, ₸</label
                  ><el-input-number
                    v-model="qrAmount"
                    :min="1"
                    :max="1000000000"
                    :precision="2"
                    :controls="false"
                    size="large"
                    placeholder="0"
                    class="wide"
                  /><el-button type="primary" size="large" class="wide submit" :loading="busy" @click="createQR"
                    ><Plus :size="17" /> Сгенерировать QR</el-button
                  >
                </div>
                <div class="tip">
                  <ShieldCheck :size="18" /> QR-код создаётся локально в браузере и не отправляется сторонним сервисам.
                </div>
              </section>
              <section class="panel qr-panel">
                <template v-if="createdQR"
                  ><div class="qr-image"><img :src="qrImage" alt="QR для оплаты Kaspi" /></div>
                  <el-tag :type="statusType(qrStatus)" effect="light" round>{{ statusText(qrStatus) }}</el-tag>
                  <h2>{{ money(qrAmount) }}</h2>
                  <small>Операция #{{ createdQR.QrOperationId }}</small
                  ><el-button plain class="wide" @click="copy(createdQR.QrToken)"
                    ><Copy :size="16" /> Скопировать ссылку</el-button
                  ></template
                ><template v-else
                  ><div class="empty-art violet"><QrCode :size="50" /></div>
                  <h2>QR появится здесь</h2>
                  <p>Введите сумму и нажмите «Сгенерировать QR».</p></template
                >
              </section>
            </div></template
          >
          <template v-if="page === 'history'"
            ><section class="panel table-panel history-panel">
              <div class="panel-header">
                <div>
                  <span class="eyebrow">ОПЕРАЦИИ KASPI</span>
                  <h2>История платежей</h2>
                </div>
                <el-input v-model="search" placeholder="Поиск по истории" clearable class="search-input"
                  ><template #prefix><Search :size="17" /></template
                ></el-input>
              </div>
              <el-table
                :data="filteredRows"
                v-loading="loading"
                empty-text="Операции не найдены"
                @row-click="openDetail"
                ><el-table-column label="Операция" min-width="150"
                  ><template #default="{ row }"
                    ><b>#{{ row.QrOperationId || row.Id || '—' }}</b
                    ><small class="row-sub">{{ row.kind }}</small></template
                  ></el-table-column
                ><el-table-column label="Клиент" min-width="140"
                  ><template #default="{ row }">{{
                    row.ClientMobile || row.ClientName || row.PhoneNumber || '—'
                  }}</template></el-table-column
                ><el-table-column label="Сумма" min-width="140"
                  ><template #default="{ row }"
                    ><b>{{ money(row.Amount || row.PaymentAmount) }}</b></template
                  ></el-table-column
                ><el-table-column label="Статус" min-width="160"
                  ><template #default="{ row }"
                    ><el-tag :type="statusType(row.Status)" effect="light" round>{{
                      statusText(row.Status)
                    }}</el-tag></template
                  ></el-table-column
                ><el-table-column label="" width="60"
                  ><template #default><ChevronRight :size="16" /></template></el-table-column
              ></el-table>
              <div class="table-foot">
                {{ filteredRows.length }} операций ·
                {{ lastRefresh ? `Обновлено ${lastRefresh.toLocaleTimeString('ru-KZ')}` : 'Данные Kaspi' }}
              </div>
            </section></template
          >
          <el-dialog :model-value="!!selectedRow" title="Детали операции" width="480px" @close="selectedRow = null"
            ><template v-if="selectedRow"
              ><div class="detail-line">
                <span>ID</span><b>{{ selectedRow.QrOperationId || selectedRow.Id || '—' }}</b>
              </div>
              <div class="detail-line">
                <span>Сумма</span><b>{{ money(selectedRow.Amount || selectedRow.PaymentAmount) }}</b>
              </div>
              <div class="detail-line">
                <span>Статус</span
                ><el-tag :type="statusType(selectedDetail?.Status || selectedRow.Status)">{{
                  statusText(selectedDetail?.Status || selectedRow.Status)
                }}</el-tag>
              </div>
              <div class="detail-line">
                <span>Клиент</span
                ><b>{{ selectedDetail?.ClientName || selectedRow.ClientName || selectedRow.ClientMobile || '—' }}</b>
              </div>
              <div class="detail-line">
                <span>Тип</span><b>{{ selectedRow.kind }}</b>
              </div>
              <div class="detail-actions">
                <el-button plain @click="copy(selectedRow.QrOperationId || selectedRow.Id)"
                  ><Copy :size="16" /> ID</el-button
                ><el-button v-if="selectedRow.kind === 'Операция'" type="primary" plain @click="refundFromDetail"
                  >Оформить возврат</el-button
                >
              </div></template
            ></el-dialog
          >
          <template v-if="page === 'refund'"
            ><div class="work-grid">
              <section class="panel form-panel">
                <div class="panel-header">
                  <div>
                    <span class="eyebrow">ВОЗВРАТ СРЕДСТВ</span>
                    <h2>Оформить возврат</h2>
                  </div>
                  <span class="form-ornament amber"><RotateCcw :size="22" /></span>
                </div>
                <p class="panel-intro">Укажите ID оплаченной QR-операции и сумму возврата.</p>
                <div class="form-fields">
                  <label class="field-label">ID операции</label
                  ><el-input v-model="refund.qrOperationId" size="large" placeholder="Например, 123456789" /><label
                    class="field-label"
                    >Сумма возврата, ₸</label
                  ><el-input-number
                    v-model="refund.returnAmount"
                    :min="1"
                    :max="1000000000"
                    :precision="2"
                    :controls="false"
                    size="large"
                    placeholder="0"
                    class="wide"
                  /><el-button type="primary" size="large" class="wide submit" :loading="busy" @click="createRefund"
                    ><RotateCcw :size="17" /> Оформить возврат</el-button
                  >
                </div>
              </section>
              <section class="panel result-panel">
                <div class="empty-art amber"><AlertCircle :size="46" /></div>
                <h2>Проверьте данные</h2>
                <p>
                  Перед отправкой запроса убедитесь, что ID операции и сумма верны. Kaspi обработает возврат по своему
                  регламенту.
                </p>
              </section>
            </div></template
          >
          <template v-if="page === 'settings'"
            ><div class="settings-grid">
              <section class="panel form-panel">
                <div class="panel-header">
                  <div>
                    <span class="eyebrow">KASPI PAY</span>
                    <h2>{{ connected ? 'Подключённый аккаунт' : 'Подключение аккаунта' }}</h2>
                  </div>
                  <span class="form-ornament"><Smartphone :size="22" /></span>
                </div>
                <template v-if="connected"
                  ><div class="connection-summary">
                    <div class="summary-icon"><CheckCircle2 :size="27" /></div>
                    <div>
                      <strong>{{ account.orgName || 'Kaspi Pay' }}</strong
                      ><span>{{ account.phone || 'Аккаунт подключён' }}</span>
                    </div>
                    <el-tag type="success" round>Активен</el-tag>
                  </div>
                  <div class="detail-line">
                    <span>Организация</span><b>{{ account.orgName || '—' }}</b>
                  </div>
                  <div class="detail-line">
                    <span>Телефон</span><b>{{ account.phone || '—' }}</b>
                  </div>
                  <div class="settings-actions">
                    <el-button :loading="busy" @click="checkSession"
                      ><RefreshCw :size="16" /> Проверить сессию</el-button
                    ><el-button type="danger" plain @click="logoutKaspi">Отключить</el-button>
                  </div>
                  <p v-if="checkResult" class="field-help">{{ checkResult }}</p></template
                ><template v-else
                  ><p class="panel-intro">Войдите через SMS, отправленное Kaspi на ваш номер.</p>
                  <div v-if="authStep === 0" class="auth-step">
                    <span class="step-circle">01</span>
                    <h3>Начните подключение</h3>
                    <p>Сервис откроет защищённую сессию Kaspi.</p>
                    <el-button type="primary" size="large" :loading="busy" @click="beginAuth"
                      >Начать подключение <ArrowUpRight :size="17"
                    /></el-button>
                  </div>
                  <div v-else-if="authStep === 1" class="form-fields">
                    <label class="field-label">Телефон Kaspi Pay</label
                    ><el-input v-model="phone" size="large" placeholder="7XXXXXXXXXX" maxlength="11" /><el-button
                      type="primary"
                      size="large"
                      class="wide submit"
                      :loading="busy"
                      @click="sendPhone"
                      >Отправить SMS</el-button
                    >
                  </div>
                  <div v-else class="form-fields">
                    <label class="field-label">Код из SMS</label
                    ><el-input v-model="otp" size="large" placeholder="Введите код" maxlength="8" /><el-button
                      type="primary"
                      size="large"
                      class="wide submit"
                      :loading="busy"
                      @click="verifyOTP"
                      >Подтвердить вход</el-button
                    >
                  </div></template
                >
              </section>
              <section class="panel result-panel">
                <div class="empty-art"><ShieldCheck :size="46" /></div>
                <h2>Доступ под контролем</h2>
                <p>
                  Панель защищена паролем. Сервисные API-ключи продолжают работать для интеграций. Сессия Kaspi хранится
                  на сервере в зашифрованном виде.
                </p>
                <button class="text-link" @click="lock">Выйти из панели <ArrowUpRight :size="16" /></button>
              </section>
            </div>
            <section class="panel monitor-panel">
              <div class="panel-header">
                <div>
                  <span class="eyebrow">МОНИТОРИНГ</span>
                  <h2>Уведомления Telegram</h2>
                </div>
                <span class="form-ornament violet"><AlertCircle :size="22" /></span>
              </div>
              <p class="panel-intro">
                Сервер регулярно проверяет подключение Kaspi, даже когда панель закрыта. При сбое и восстановлении
                отправляет сообщение боту.
              </p>
              <div class="detail-line">
                <span>Проверка Kaspi</span
                ><el-tag
                  :type="
                    monitorInfo.status === 'connected'
                      ? 'success'
                      : monitorInfo.status === 'disconnected'
                        ? 'danger'
                        : 'info'
                  "
                  round
                  >{{
                    monitorInfo.status === 'connected'
                      ? 'Подключено'
                      : monitorInfo.status === 'disconnected'
                        ? 'Связь потеряна'
                        : monitorInfo.status === 'not_connected'
                          ? 'Ожидает подключения'
                          : 'Проверяется'
                  }}</el-tag
                >
              </div>
              <p v-if="monitorInfo.status === 'disconnected'" class="field-help">
                {{
                  monitorInfo.reason === 'kaspi_rejected'
                    ? 'Kaspi отклонил сессию. Войдите повторно по SMS.'
                    : monitorInfo.reason === 'session_missing'
                      ? 'Сессия Kaspi отсутствует. Подключите аккаунт снова.'
                      : 'Не удалось связаться с Kaspi. Проверьте сеть.'
                }}
              </p>
              <div class="detail-line">
                <span>Telegram</span><b>{{ monitorInfo.telegramConfigured ? 'Настроен' : 'Не настроен в .env' }}</b>
              </div>
              <div v-if="monitorInfo.intervalSeconds" class="detail-line">
                <span>Интервал проверки</span><b>{{ monitorInfo.intervalSeconds }} сек</b>
              </div>
              <div v-if="monitorInfo.lastChecked" class="detail-line">
                <span>Последняя проверка</span><b>{{ new Date(monitorInfo.lastChecked).toLocaleString('ru-KZ') }}</b>
              </div>
              <div class="settings-actions">
                <el-button :loading="busy" :disabled="!monitorInfo.telegramConfigured" @click="testTelegram"
                  ><Send :size="16" /> Отправить тест</el-button
                ><el-button @click="loadMonitor"><RefreshCw :size="16" /> Обновить</el-button>
              </div>
              <p v-if="!monitorInfo.telegramConfigured" class="field-help">
                Добавьте TELEGRAM_BOT_TOKEN и TELEGRAM_CHAT_ID в .env и перезапустите сервер.
              </p>
            </section>
          </template>
        </template>
      </main>
      <footer class="footer">Kaspi POS Console <span>·</span> Данные операций предоставлены Kaspi Pay</footer>
    </div>
  </div>
</template>
