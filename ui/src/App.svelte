<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { runAroniumCLI } from './lib/ipc';
  import type {
    AccountMovementItemInput,
    AccountsLedgerReport,
    BackupManifest,
    CatalogProduct,
    CatalogSearchResponse,
    ConfigInfoResponse,
    CustomerAccount,
    DailyClosingReport,
    DashboardSnapshot,
    DoctorReport,
    DriveStatus,
    InventoryItem,
    KardexEntry,
    SyncResult,
    UpdateApplyResult,
    UpdateCheckResult,
  } from './lib/types';

  type UIMode = 'basic' | 'advanced';
  type BasicTab = 'create' | 'accounts' | 'inventory' | 'setup';
  type AdvTab = 'dashboard' | 'accounts' | 'inventory' | 'registers' | 'drive' | 'doctor';

  let uiMode = $state<UIMode>('basic');
  let basicTab = $state<BasicTab>('create');
  let advTab = $state<AdvTab>('dashboard');
  let loading = $state(false);
  let toastMessage = $state('');
  let toastError = $state(false);
  let showDbModal = $state(false);

  // Live CLI JSON data models
  let configInfo = $state<ConfigInfoResponse | null>(null);
  let doctor = $state<DoctorReport | null>(null);
  let accountsReport = $state<AccountsLedgerReport | null>(null);
  let snapshot = $state<DashboardSnapshot | null>(null);
  let dailyReport = $state<DailyClosingReport | null>(null);
  let driveStatus = $state<DriveStatus | null>(null);
  let kardexList = $state<KardexEntry[]>([]);
  let lastSyncResult = $state<SyncResult | null>(null);
  let lastBackup = $state<BackupManifest | null>(null);
  let serviceStatus = $state<{ status: string; log_path: string } | null>(null);
  let updateCheck = $state<UpdateCheckResult | null>(null);
  let updateApplyResult = $state<UpdateApplyResult | null>(null);
  let checkingUpdate = $state(false);
  let applyingUpdate = $state(false);

  // Database path selector inputs
  let customPosDbPath = $state('');
  let customAccountsDbPath = $state('');

  // Create Account / Annotate Items Form (Modo Básico)
  let accountActionMode = $state<'new' | 'existing'>('new');
  let selectedExistingAccountKey = $state('');
  let formIdPrefix = $state('');
  let formIdNumber = $state('');
  let formIdNumberEl = $state<HTMLInputElement | null>(null);
  let formName = $state('');
  let formPhone = $state('');
  let formPaymentMethod = $state('Efectivo');
  let formCreditLimit = $state<number>(500000);
  let formDescription = $state('');
  let formMoveNote = $state('');
  let allowOverLimitConfirm = $state(false);

  let combinedFormId = $derived(
    formIdPrefix.trim() && formIdNumber.trim()
      ? `${formIdPrefix.trim()}-${formIdNumber.trim()}`
      : formIdPrefix.trim()
        ? `${formIdPrefix.trim()}-`
        : formIdNumber.trim()
  );

  // Quick Punctual Value entry above scanner (Dulces, comida, tinto / sin código)
  let quickValueDesc = $state('');
  let quickValueAmount = $state<number>(0);

  // Barcode Scanner & Live Product Search (from pos.db)
  let scannerInputEl = $state<HTMLInputElement | null>(null);
  let scannerQuery = $state('');
  let catalogMatches = $state<CatalogProduct[]>([]);
  let catalogSearching = $state(false);
  let cartItems = $state<AccountMovementItemInput[]>([]);

  // Accounts list / Payment modal
  let accountSearch = $state('');
  let activePayAccount = $state<CustomerAccount | null>(null);
  let payAmount = $state<number>(0);
  let payMethod = $state('Efectivo');
  let payNote = $state('Pago recibido en cuenta');
  let expandedAccountKey = $state<string>('');
  let selectedStatement = $state('');
  let selectedStatementCustomer = $state('');

  // Inventory Filters
  let invSearch = $state('');
  let invStatusFilter = $state<'ALL' | 'OK' | 'STOCK_BAJO' | 'AGOTADO' | 'STOCK_NEGATIVO'>('ALL');
  let invSubView = $state<'catalog' | 'kardex'>('catalog');
  let backupPassphrase = $state('');

  function accKeyOf(acc: CustomerAccount): string {
    return acc.account_key || acc.id;
  }

  let selectedExistingAccount = $derived(
    (accountsReport?.cuentas || []).find((a) => accKeyOf(a) === selectedExistingAccountKey) || null
  );

  let accountsSharingNewId = $derived.by(() => {
    const target =
      formIdPrefix.trim() && formIdNumber.trim()
        ? `${formIdPrefix.trim()}-${formIdNumber.trim()}`.toLowerCase()
        : '';
    if (!target) return [];
    return (accountsReport?.cuentas || []).filter((a) => a.id.toLowerCase() === target);
  });

  let sharedAccountsForPayment = $derived.by(() => {
    if (!activePayAccount) return [];
    const targetId = activePayAccount.id.toLowerCase();
    return (accountsReport?.cuentas || []).filter((a) => a.id.toLowerCase() === targetId);
  });

  let cartTotal = $derived(
    cartItems.reduce(
      (sum, it) => sum + Math.round((it.cantidad || 1) * (it.precio_unitario_snapshot || 0)),
      0
    )
  );

  let currentAccountBalance = $derived.by(() => {
    if (accountActionMode === 'existing' && selectedExistingAccount) {
      return selectedExistingAccount.saldo_actual;
    }
    const targetId =
      formIdPrefix.trim() && formIdNumber.trim()
        ? `${formIdPrefix.trim()}-${formIdNumber.trim()}`.toLowerCase()
        : '';
    const targetName = formName.trim().toLowerCase();
    if (!targetId || !targetName) return 0;
    return (
      (accountsReport?.cuentas || []).find(
        (a) => a.id.toLowerCase() === targetId && a.nombre.toLowerCase() === targetName
      )?.saldo_actual || 0
    );
  });

  let activeCreditLimit = $derived(
    accountActionMode === 'existing' && selectedExistingAccount
      ? selectedExistingAccount.tope_credito
      : Number(formCreditLimit || 0)
  );

  let projectedBalance = $derived(currentAccountBalance + cartTotal);
  let exceedsProjectedLimit = $derived(
    activeCreditLimit > 0 && projectedBalance > activeCreditLimit
  );
  let limitUsagePct = $derived(
    activeCreditLimit > 0 ? Math.min(100, (projectedBalance / activeCreditLimit) * 100) : 0
  );

  function setUIMode(mode: UIMode) {
    uiMode = mode;
    try {
      localStorage.setItem('aronium_ui_mode', mode);
    } catch {
      // ignore storage errors
    }
  }

  function showToast(msg: string, isErr = false) {
    toastMessage = msg;
    toastError = isErr;
  }

  async function refreshAll() {
    loading = true;
    try {
      const [cfgRes, docRes, accRes, drvRes, srvRes] = await Promise.all([
        runAroniumCLI<ConfigInfoResponse>(['config', 'get', '--json']),
        runAroniumCLI<DoctorReport>(['doctor', '--json']),
        runAroniumCLI<AccountsLedgerReport>(['accounts', 'list', '--json']),
        runAroniumCLI<DriveStatus>(['drive', 'status', '--json']),
        runAroniumCLI<{ status: string; log_path: string }>(['service', 'status', '--json']),
      ]);

      configInfo = cfgRes;
      doctor = docRes;
      accountsReport = accRes;
      driveStatus = drvRes;
      serviceStatus = srvRes;

      if (!customPosDbPath) customPosDbPath = cfgRes.config.pos_db_path;
      if (!customAccountsDbPath) customAccountsDbPath = cfgRes.config.accounts_db_path;

      if (docRes.pos_db_readable) {
        const [snap, daily] = await Promise.all([
          runAroniumCLI<DashboardSnapshot>(['admin', '--json']),
          runAroniumCLI<DailyClosingReport>(['reports', 'daily', '--deliver=false', '--json']),
        ]);
        snapshot = snap;
        dailyReport = daily;

        // Preload initial catalog matches so products appear immediately
        await searchCatalog('');
      } else {
        snapshot = null;
        dailyReport = null;
        catalogMatches = [];
      }
    } catch (e: any) {
      showToast(`No se pudo comunicar con el motor aronium-util: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function searchCatalog(queryText: string) {
    if (!doctor?.pos_db_readable) return;
    catalogSearching = true;
    try {
      const res = await runAroniumCLI<CatalogSearchResponse>([
        'catalog',
        'search',
        '--query',
        queryText,
        '--limit',
        '30',
        '--json',
      ]);
      catalogMatches = res.matches || [];
    } catch {
      // ignore transient search errors
    } finally {
      catalogSearching = false;
    }
  }

  function addProductToCart(prod: CatalogProduct) {
    const unitPrice = Math.round(prod.price);
    const existingIdx = cartItems.findIndex(
      (it) => it.aronium_product_id === prod.id && it.precio_unitario_snapshot === unitPrice
    );
    if (existingIdx >= 0) {
      cartItems[existingIdx] = {
        ...cartItems[existingIdx],
        cantidad: Number(cartItems[existingIdx].cantidad) + 1,
      };
      cartItems = [...cartItems];
    } else {
      cartItems = [
        ...cartItems,
        {
          aronium_product_id: prod.id,
          codigo_barras: prod.barcode || prod.code || '',
          nombre_snapshot: prod.name,
          precio_unitario_snapshot: unitPrice,
          cantidad: 1,
        },
      ];
    }
  }

  async function handleScannerKeydown(event: KeyboardEvent) {
    if (event.key !== 'Enter') return;
    event.preventDefault();
    const rawCode = scannerQuery.trim();
    if (!rawCode) return;

    if (!doctor?.pos_db_readable) {
      showToast(
        'Primero conecta el archivo pos.db de Aronium para buscar productos por código de barras.',
        true
      );
      return;
    }

    try {
      const res = await runAroniumCLI<CatalogSearchResponse>([
        'catalog',
        'search',
        '--barcode',
        rawCode,
        '--query',
        rawCode,
        '--limit',
        '15',
        '--json',
      ]);

      if (res.exact_match) {
        addProductToCart(res.exact_match);
        scannerQuery = '';
        await searchCatalog('');
        showToast(`+1 "${res.exact_match.name}" agregado ($${Math.round(res.exact_match.price).toLocaleString()})`);
      } else if (res.matches && res.matches.length === 1) {
        addProductToCart(res.matches[0]);
        scannerQuery = '';
        await searchCatalog('');
        showToast(`+1 "${res.matches[0].name}" agregado`);
      } else {
        catalogMatches = res.matches || [];
        if (catalogMatches.length === 0) {
          showToast(`No encontramos ningún producto con código o nombre "${rawCode}" en Aronium.`, true);
        }
      }
    } catch (e: any) {
      showToast(`Error buscando código "${rawCode}": ${e?.message || e}`, true);
    } finally {
      await tick();
      scannerInputEl?.focus();
    }
  }

  function handleIdPrefixInput(value: string) {
    if (value.includes('-')) {
      const idx = value.lastIndexOf('-');
      formIdPrefix = value.slice(0, idx).replace(/-/g, '').trim();
      const restDigits = value.slice(idx + 1).replace(/\D/g, '');
      if (restDigits) {
        formIdNumber = restDigits;
      }
      tick().then(() => formIdNumberEl?.focus());
    } else {
      formIdPrefix = value;
    }
  }

  function handleIdNumberInput(value: string) {
    formIdNumber = value.replace(/\D/g, '');
  }

  function adjustQuickValue(delta: number) {
    const cur = Math.round(Number(quickValueAmount || 0));
    quickValueAmount = Math.max(0, cur + delta);
  }

  function addQuickValueToCart(rawAmount: number) {
    const val = Math.round(Number(rawAmount || 0));
    if (val <= 0) {
      showToast('Ingresa o selecciona un valor puntual mayor a $0.', true);
      return;
    }
    const label = quickValueDesc.trim() || 'Valor puntual (varios / sin código)';
    const existingIdx = cartItems.findIndex(
      (it) =>
        it.aronium_product_id === 0 &&
        it.codigo_barras === 'PUNTUAL' &&
        it.nombre_snapshot === label &&
        it.precio_unitario_snapshot === val
    );
    if (existingIdx >= 0) {
      cartItems[existingIdx] = {
        ...cartItems[existingIdx],
        cantidad: Number(cartItems[existingIdx].cantidad) + 1,
      };
      cartItems = [...cartItems];
    } else {
      cartItems = [
        ...cartItems,
        {
          aronium_product_id: 0,
          codigo_barras: 'PUNTUAL',
          nombre_snapshot: label,
          precio_unitario_snapshot: val,
          cantidad: 1,
        },
      ];
    }
    showToast(`+$${val.toLocaleString()} agregado: "${label}"`);
  }

  function adjustCartItemUnitPrice(index: number, delta: number) {
    const curPrice = Math.round(Number(cartItems[index].precio_unitario_snapshot || 0));
    const nextPrice = curPrice + delta;
    if (nextPrice <= 0) {
      cartItems = cartItems.filter((_, i) => i !== index);
      return;
    }
    cartItems[index] = {
      ...cartItems[index],
      precio_unitario_snapshot: nextPrice,
    };
    cartItems = [...cartItems];
  }

  function updateItemQty(index: number, delta: number) {
    const nextQty = Number(cartItems[index].cantidad) + delta;
    if (nextQty <= 0) {
      cartItems = cartItems.filter((_, i) => i !== index);
      return;
    }
    cartItems[index] = { ...cartItems[index], cantidad: nextQty };
    cartItems = [...cartItems];
  }

  function selectExistingAccountForCharge(acc: CustomerAccount) {
    accountActionMode = 'existing';
    selectedExistingAccountKey = accKeyOf(acc);
    if (acc.id_prefix || acc.id_number) {
      formIdPrefix = acc.id_prefix || '';
      formIdNumber = acc.id_number || '';
    } else if (acc.id.includes('-')) {
      const idx = acc.id.lastIndexOf('-');
      formIdPrefix = acc.id.slice(0, idx);
      formIdNumber = acc.id.slice(idx + 1);
    } else {
      formIdPrefix = '';
      formIdNumber = acc.id;
    }
    formName = acc.nombre;
    formPhone = acc.telefono;
    formPaymentMethod = acc.metodo_pago;
    formCreditLimit = acc.tope_credito;
    formDescription = acc.descripcion;
    allowOverLimitConfirm = false;
  }

  async function handleSaveAccountAndItems() {
    const cleanPrefix = formIdPrefix.trim();
    const cleanNum = formIdNumber.trim();
    if (accountActionMode === 'new') {
      if (!cleanPrefix || !cleanNum || !/^\d+$/.test(cleanNum) || !formName.trim()) {
        showToast(
          'Por favor completa las 2 partes del ID (Texto y Número, ej. Parques-18502) y el Nombre del cliente. Teléfono y descripción son opcionales.',
          true
        );
        return;
      }
    } else if (!selectedExistingAccountKey.trim()) {
      showToast('Selecciona la cuenta del cliente a la que deseas anotar artículos.', true);
      return;
    }

    if (exceedsProjectedLimit && !allowOverLimitConfirm) {
      showToast(
        `El saldo proyectado ($${projectedBalance.toLocaleString()}) supera el tope de crédito ($${activeCreditLimit.toLocaleString()}). Marca la casilla de confirmación si deseas autorizarlo.`,
        true
      );
      return;
    }

    loading = true;
    try {
      if (accountActionMode === 'new') {
        const fullId = `${cleanPrefix}-${cleanNum}`;
        const args = [
          'accounts',
          'create',
          '--id',
          fullId,
          '--id-prefix',
          cleanPrefix,
          '--id-number',
          cleanNum,
          '--name',
          formName.trim(),
          '--phone',
          formPhone.trim(),
          '--payment-method',
          formPaymentMethod,
          '--limit',
          String(Math.round(Number(formCreditLimit || 0))),
          '--description',
          formDescription.trim(),
        ];
        if (formMoveNote.trim()) {
          args.push('--note', formMoveNote.trim());
        }
        if (cartItems.length > 0) {
          args.push('--items-json', JSON.stringify(cartItems));
        }
        args.push('--json');
        await runAroniumCLI(args);
        showToast(
          `¡Cuenta [${fullId}] "${formName.trim()}" guardada en cuentas.db${cartItems.length > 0 ? ` con ${cartItems.length} concepto(s)` : ''} y sincronizada con Google Drive!`
        );
      } else {
        if (cartItems.length === 0) {
          showToast('Escanea, agrega un artículo o ingresa un valor puntual para anotar en la cuenta.', true);
          loading = false;
          return;
        }
        const args = [
          'accounts',
          'charge',
          '--account-id',
          selectedExistingAccountKey,
          '--name',
          selectedExistingAccount?.nombre || '',
          '--items-json',
          JSON.stringify(cartItems),
          '--payment-method',
          formPaymentMethod,
          '--note',
          formMoveNote.trim() || `Anotación de ${cartItems.length} concepto(s)`,
          '--json',
        ];
        await runAroniumCLI(args);
        showToast(
          `¡Conceptos anotados a la cuenta [${selectedExistingAccount?.id}] de ${selectedExistingAccount?.nombre} y sincronizados con Google Drive!`
        );
      }

      cartItems = [];
      quickValueDesc = '';
      quickValueAmount = 0;
      formMoveNote = '';
      allowOverLimitConfirm = false;
      if (accountActionMode === 'new') {
        formIdPrefix = '';
        formIdNumber = '';
        formName = '';
        formPhone = '';
        formDescription = '';
      }
      await refreshAll();
      await tick();
      scannerInputEl?.focus();
    } catch (e: any) {
      showToast(`Error guardando cuenta: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleRecordPayment() {
    if (!activePayAccount || !payAmount || payAmount <= 0) {
      showToast('Ingresa un monto de pago válido mayor a 0.', true);
      return;
    }
    loading = true;
    try {
      await runAroniumCLI([
        'accounts',
        'pay',
        '--account-id',
        accKeyOf(activePayAccount),
        '--name',
        activePayAccount.nombre,
        '--amount',
        String(Math.round(payAmount)),
        '--payment-method',
        payMethod,
        '--note',
        payNote.trim() || 'Abono / pago en cuenta de cliente',
        '--json',
      ]);
      const paidCustomer = activePayAccount.nombre;
      const paidId = activePayAccount.id;
      activePayAccount = null;
      payAmount = 0;
      await refreshAll();
      showToast(
        `¡Pago registrado para [${paidId}] ${paidCustomer} en cuentas.db y sincronizado con Google Drive (el cierre diario de Aronium no fue alterado)!`
      );
    } catch (e: any) {
      showToast(`Error registrando pago: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleViewStatement(accKey: string, customerName: string) {
    loading = true;
    try {
      const res = await runAroniumCLI<{ statement: string }>([
        'accounts',
        'statement',
        '--account-id',
        accKey,
        '--name',
        customerName,
        '--json',
      ]);
      selectedStatementCustomer = customerName;
      selectedStatement = res.statement;
    } catch (e: any) {
      showToast(`Error generando recibo de cuenta: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handlePickDatabase(kind: 'pos' | 'accounts') {
    try {
      const res = await runAroniumCLI<{ selected: boolean; path: string }>([
        'config',
        'pick-db',
        '--kind',
        kind,
        '--json',
      ]);
      if (res.selected && res.path) {
        if (kind === 'pos') {
          customPosDbPath = res.path;
        } else {
          customAccountsDbPath = res.path;
        }
        await handleSaveDatabasePaths(false, false);
      }
    } catch (e: any) {
      showToast(`No se pudo abrir el selector gráfico: ${e?.message || e}. Puedes pegar la ruta manualmente.`, true);
    }
  }

  async function handleSaveDatabasePaths(useDefaultPos = false, createDemoPos = false) {
    loading = true;
    try {
      const args = ['config', 'set-db'];
      if (useDefaultPos) {
        args.push('--use-default-pos');
      } else if (customPosDbPath.trim()) {
        args.push('--set-pos-db', customPosDbPath.trim());
      }
      if (customAccountsDbPath.trim()) {
        args.push('--set-accounts-db', customAccountsDbPath.trim());
      }
      if (createDemoPos) {
        args.push('--create-demo-pos');
      }
      args.push('--json');

      const res = await runAroniumCLI<{ config: any; doctor: DoctorReport }>(args);
      customPosDbPath = res.config.pos_db_path;
      customAccountsDbPath = res.config.accounts_db_path;
      showDbModal = false;
      await refreshAll();
      if (res.doctor?.pos_db_readable) {
        showToast('¡Bases de datos conectadas correctamente!');
      } else {
        showToast(
          'Se guardó la ruta, pero aún no encontramos el archivo de Aronium (pos.db) en esa ubicación.',
          true
        );
      }
    } catch (e: any) {
      showToast(`Error configurando bases de datos: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleSyncDrive() {
    loading = true;
    try {
      lastSyncResult = await runAroniumCLI<SyncResult>(['sync', '--json']);
      await refreshAll();
      showToast(`Sincronización completa guardada en: ${lastSyncResult.master_workbook_path}`);
    } catch (e: any) {
      showToast(`Error sincronizando con Google Drive: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleDriveWebLogin() {
    showToast('Abriendo tu navegador web para iniciar sesión con Google Drive...');
    try {
      await runAroniumCLI(['drive', 'login', '--timeout', '120', '--json']);
      await refreshAll();
      showToast('¡Tu cuenta de Google Drive quedó conectada exitosamente!');
    } catch (e: any) {
      showToast(`Autenticación web pendiente o cancelada: ${e?.message || e}`, true);
    }
  }

  async function loadKardex() {
    if (!doctor?.pos_db_readable) return;
    try {
      kardexList = await runAroniumCLI<KardexEntry[]>(['inventory', 'kardex', '--json']);
    } catch (e: any) {
      showToast(`Error cargando Kardex: ${e?.message || e}`, true);
    }
  }

  async function handleCreateBackup() {
    loading = true;
    try {
      const args = ['backup', 'create', '--json'];
      if (backupPassphrase.trim()) {
        args.push('--passphrase', backupPassphrase.trim());
      }
      lastBackup = await runAroniumCLI<BackupManifest>(args);
      showToast(`Copia de seguridad creada en: ${lastBackup.archive_path}`);
    } catch (e: any) {
      showToast(`Error creando copia de seguridad: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleInstallAutoStartService() {
    loading = true;
    try {
      const res = await runAroniumCLI<{ result: string }>(['service', 'install', '--json']);
      await refreshAll();
      showToast(`Inicio automático configurado: ${res.result}`);
    } catch (e: any) {
      showToast(`Error registrando inicio automático: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleCheckForUpdate(silent = false) {
    checkingUpdate = true;
    try {
      const res = await runAroniumCLI<UpdateCheckResult>(['update', 'check', '--json']);
      updateCheck = res;
      if (!silent) {
        if (res.update_available) {
          showToast(
            `¡Nueva versión disponible en GitHub (${res.tag_name})! Versión actual: v${res.current_version}.`
          );
        } else {
          showToast(`Ya tienes instalada la versión más reciente (v${res.current_version}).`);
        }
      }
    } catch (e: any) {
      if (!silent) {
        showToast(`No se pudo verificar actualizaciones en GitHub: ${e?.message || e}`, true);
      }
    } finally {
      checkingUpdate = false;
    }
  }

  async function handleApplyUpdate(force = false) {
    applyingUpdate = true;
    try {
      const args = ['update', 'apply', '--launch-ui=true', '--json'];
      if (force) {
        args.push('--force');
      }
      const res = await runAroniumCLI<UpdateApplyResult>(args);
      updateApplyResult = res;
      showToast(res.message);
    } catch (e: any) {
      showToast(`Error aplicando actualización desde GitHub: ${e?.message || e}`, true);
    } finally {
      applyingUpdate = false;
    }
  }

  function friendlyStockStatus(status: string): string {
    if (status === 'OK') return 'Disponible';
    if (status === 'STOCK_BAJO') return 'Stock Bajo';
    if (status === 'AGOTADO') return 'Agotado';
    if (status === 'STOCK_NEGATIVO') return 'Stock Negativo';
    return status;
  }

  function statusBadgeClass(status: string): string {
    if (status === 'OK' || status === 'ACTIVA' || status === 'AL_DIA' || status === 'PAGADO')
      return 'badge badge-ok';
    if (status === 'STOCK_BAJO' || status === 'PENDIENTE' || status === 'ABONADO_PARCIAL')
      return 'badge badge-warn';
    return 'badge badge-danger';
  }

  function filteredAccounts(): CustomerAccount[] {
    const list = accountsReport?.cuentas || [];
    const q = accountSearch.trim().toLowerCase();
    if (!q) return list;
    return list.filter(
      (a) =>
        a.nombre.toLowerCase().includes(q) ||
        a.id.toLowerCase().includes(q) ||
        (a.telefono && a.telefono.toLowerCase().includes(q))
    );
  }

  function filteredInventory(): InventoryItem[] {
    const list = snapshot?.inventory_summary?.items || [];
    const q = invSearch.trim().toLowerCase();
    return list.filter((item) => {
      if (invStatusFilter !== 'ALL' && item.status !== invStatusFilter) return false;
      if (!q) return true;
      return (
        item.name.toLowerCase().includes(q) ||
        item.code.toLowerCase().includes(q) ||
        item.barcode.toLowerCase().includes(q) ||
        item.category.toLowerCase().includes(q)
      );
    });
  }

  onMount(() => {
    try {
      const savedMode = localStorage.getItem('aronium_ui_mode');
      if (savedMode === 'basic' || savedMode === 'advanced') {
        uiMode = savedMode;
      }
    } catch {
      // ignore
    }
    refreshAll();
  });
</script>

<div class="app-shell">
  <!-- GLOBAL TOP HEADER WITH BASIC / ADVANCED SWITCH & DB STATUS -->
  <header class="mode-header">
    <div class="mode-brand">
      <div class="brand-icon">AU</div>
      <div>
        <div class="brand-title">Aronium Utility UI</div>
        <div class="brand-sub">
          {snapshot?.store_name || 'Gestión de Cuentas de Clientes & Inventario'}
        </div>
      </div>
    </div>

    <!-- Prominent Basic / Advanced Switch at the top -->
    <div class="mode-switch-group" role="group" aria-label="Selector de Modo de Interfaz">
      <button
        class="mode-switch-btn {uiMode === 'basic' ? 'active-basic' : ''}"
        onclick={() => setUIMode('basic')}
      >
        Modo Básico (Tienda)
      </button>
      <button
        class="mode-switch-btn {uiMode === 'advanced' ? 'active-adv' : ''}"
        onclick={() => setUIMode('advanced')}
      >
        Modo Avanzado (Técnico)
      </button>
    </div>

    <div class="db-status-pills">
      <span class={doctor?.pos_db_readable ? 'badge badge-ok' : 'badge badge-danger'}>
        {doctor?.pos_db_readable ? '✓ Productos Aronium OK' : '⚠ Falta Base de Productos'}
      </span>
      <span class={doctor?.accounts_db_ready ? 'badge badge-ok' : 'badge badge-warn'}>
        {doctor?.accounts_db_ready ? '✓ Base Cuentas Lista' : 'Base Cuentas Pendiente'}
      </span>
      <button class="btn btn-outline" style="padding: 6px 12px;" onclick={() => (showDbModal = !showDbModal)}>
        Seleccionar Base de Datos
      </button>
    </div>
  </header>

  <!-- FRIENDLY NON-TECHNICAL ALERT IF pos.db IS NOT FOUND AT DEFAULT PATH -->
  {#if doctor && !doctor.pos_db_readable}
    <div style="padding: 14px 24px 0 24px;">
      <div class="alert-banner">
        <div style="flex: 1; min-width: 280px;">
          <strong style="font-size: 14.5px;">
            No encontramos la base de productos de Aronium en la ruta por defecto
          </strong>
          <div style="font-size: 13px; margin-top: 4px; color: #fef3c7;">
            Buscamos en <code>{doctor.pos_db_path}</code>. Si Aronium está guardado en otra carpeta de tu PC,
            haz clic en <strong>"Elegir archivo pos.db"</strong> para seleccionarlo y ver tus productos al
            instante. Tus cuentas de clientes en <code>cuentas.db</code> ya están listas para usarse.
          </div>
        </div>
        <div style="display: flex; gap: 8px; flex-wrap: wrap;">
          <button class="btn btn-amber" onclick={() => handlePickDatabase('pos')}>
            Elegir archivo pos.db en mi PC...
          </button>
          <button class="btn btn-outline" onclick={() => handleSaveDatabasePaths(true, false)}>
            Reintentar Ruta por Defecto
          </button>
          <button class="btn btn-outline" onclick={() => handleSaveDatabasePaths(false, true)}>
            Crear Base de Prueba
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- DATABASE SELECTOR DRAWER -->
  {#if showDbModal}
    <div style="padding: 14px 24px 0 24px;">
      <div class="card" style="border-color: var(--accent-blue); margin-bottom: 10px;">
        <div class="card-title">
          <span>Configurar Bases de Datos (Productos de Aronium y Cuentas de Clientes)</span>
          <button class="btn btn-outline" style="padding: 4px 10px;" onclick={() => (showDbModal = false)}>
            Cerrar
          </button>
        </div>
        <div class="panel-grid" style="margin-bottom: 10px;">
          <div>
            <label class="field-label">
              Base de Productos de Aronium (pos.db — Solo Lectura)
              <div style="display: flex; gap: 8px; margin-top: 4px;">
                <input class="input" bind:value={customPosDbPath} placeholder="C:\Users\...\AppData\Local\Aronium\Data\pos.db" />
                <button class="btn btn-outline" onclick={() => handlePickDatabase('pos')}>Buscar...</button>
              </div>
            </label>
          </div>
          <div>
            <label class="field-label">
              Base Dedicada de Cuentas de Clientes (cuentas.db)
              <div style="display: flex; gap: 8px; margin-top: 4px;">
                <input class="input" bind:value={customAccountsDbPath} placeholder="C:\Users\...\AppData\Local\AroniumUtility\cuentas.db" />
                <button class="btn btn-outline" onclick={() => handlePickDatabase('accounts')}>Buscar...</button>
              </div>
            </label>
          </div>
        </div>
        <div style="display: flex; gap: 10px; flex-wrap: wrap;">
          <button class="btn btn-green" disabled={loading} onclick={() => handleSaveDatabasePaths(false, false)}>
            Guardar y Conectar Bases de Datos
          </button>
          <button class="btn btn-outline" disabled={loading} onclick={() => handleSaveDatabasePaths(true, false)}>
            Usar Ruta por Defecto de Aronium
          </button>
          <button class="btn btn-outline" disabled={loading} onclick={() => handleSaveDatabasePaths(false, true)}>
            Crear Base de Productos de Prueba
          </button>
        </div>
      </div>
    </div>
  {/if}

  {#if toastMessage}
    <div style="padding: 12px 24px 0 24px;">
      <div class="toast {toastError ? 'toast-err' : ''}" style="margin-bottom: 0;">
        <span>{toastMessage}</span>
        <button class="btn btn-outline" style="padding: 4px 10px;" onclick={() => (toastMessage = '')}>
          Cerrar
        </button>
      </div>
    </div>
  {/if}

  <!-- ========================================================================= -->
  <!-- MODO BÁSICO (PARA PERSONA CON CERO CONOCIMIENTO TÉCNICO)                   -->
  <!-- ========================================================================= -->
  {#if uiMode === 'basic'}
    <div class="basic-container">
      <nav class="basic-nav">
        <button
          class="basic-nav-btn {basicTab === 'create' ? 'active' : ''}"
          onclick={async () => {
            basicTab = 'create';
            await tick();
            scannerInputEl?.focus();
          }}
        >
          <span>1. Crear Cuenta / Anotar Artículos</span>
          <span>🛒</span>
        </button>

        <button
          class="basic-nav-btn {basicTab === 'accounts' ? 'active' : ''}"
          onclick={() => (basicTab = 'accounts')}
        >
          <span>2. Ver Cuentas y Registrar Pagos</span>
          <span>💵</span>
        </button>

        <button
          class="basic-nav-btn {basicTab === 'inventory' ? 'active' : ''}"
          onclick={() => (basicTab = 'inventory')}
        >
          <span>3. Ver Inventario Real Disponible</span>
          <span>📦</span>
        </button>

        <button
          class="basic-nav-btn {basicTab === 'setup' ? 'active' : ''}"
          onclick={() => (basicTab = 'setup')}
        >
          <span>4. Google Drive y Respaldo</span>
          <span>☁️</span>
        </button>
      </nav>

      <!-- BASIC TAB 1: CREATE ACCOUNT / ANNOTATE ITEMS WITH BARCODE SCANNER -->
      {#if basicTab === 'create'}
        <div class="panel-grid" style="grid-template-columns: 1.05fr 1.15fr; align-items: start;">
          <!-- Left Card: Customer Account Form -->
          <div class="card">
            <div class="card-title">
              <span>Datos de la Cuenta del Cliente</span>
              <div style="display: flex; gap: 6px;">
                <button
                  class="btn {accountActionMode === 'new' ? 'btn-green' : 'btn-outline'}"
                  style="padding: 6px 12px; font-size: 12px;"
                  onclick={() => {
                    accountActionMode = 'new';
                    selectedExistingAccountKey = '';
                    allowOverLimitConfirm = false;
                  }}
                >
                  Nueva Cuenta
                </button>
                <button
                  class="btn {accountActionMode === 'existing' ? 'btn-green' : 'btn-outline'}"
                  style="padding: 6px 12px; font-size: 12px;"
                  onclick={() => {
                    accountActionMode = 'existing';
                    allowOverLimitConfirm = false;
                  }}
                >
                  Cuenta Existente
                </button>
              </div>
            </div>

            {#if accountActionMode === 'existing'}
              <div style="margin-bottom: 14px;">
                <label class="field-label">
                  Selecciona el Cliente Existente
                  <select
                    class="select"
                    value={selectedExistingAccountKey}
                    onchange={(e) => {
                      const val = (e.currentTarget as HTMLSelectElement).value;
                      const found = (accountsReport?.cuentas || []).find((a) => accKeyOf(a) === val);
                      if (found) selectExistingAccountForCharge(found);
                    }}
                  >
                    <option value="">-- Elige un cliente --</option>
                    {#each accountsReport?.cuentas || [] as acc}
                      <option value={accKeyOf(acc)}>
                        [{acc.id}] {acc.nombre} — Saldo actual: ${acc.saldo_actual.toLocaleString()}
                      </option>
                    {/each}
                  </select>
                </label>
              </div>
            {:else}
              <div class="form-grid">
                <div class="field-label" style="grid-column: span 2;">
                  <span>ID de la Cuenta / Apartamento (Texto - Número) *</span>
                  <div class="compound-id-box">
                    <input
                      class="compound-id-part"
                      placeholder="Texto (Ej. Parques)"
                      value={formIdPrefix}
                      oninput={(e) => handleIdPrefixInput((e.currentTarget as HTMLInputElement).value)}
                    />
                    <span class="compound-id-sep">-</span>
                    <input
                      bind:this={formIdNumberEl}
                      class="compound-id-part"
                      inputmode="numeric"
                      placeholder="Número (Ej. 18502)"
                      value={formIdNumber}
                      oninput={(e) => handleIdNumberInput((e.currentTarget as HTMLInputElement).value)}
                    />
                  </div>
                  <div style="display: flex; align-items: center; gap: 10px; flex-wrap: wrap;">
                    <span class="id-preview-pill">
                      Vista previa ID: {combinedFormId || 'Texto-Número'}
                    </span>
                    {#if accountsSharingNewId.length > 0}
                      <span style="font-size: 11.5px; color: var(--accent-amber); margin-top: 6px;">
                        Ya existe cuenta en {combinedFormId} ({accountsSharingNewId.map((a) => a.nombre).join(', ')}). Usa un Nombre distinto para crear otra cuenta en este mismo ID.
                      </span>
                    {/if}
                  </div>
                </div>
                <label class="field-label">
                  Nombre Completo (Separa cuentas con mismo ID) *
                  <input class="input" placeholder="Ej. Juan Pérez" bind:value={formName} />
                </label>
                <label class="field-label">
                  Teléfono (Opcional)
                  <input class="input" placeholder="Opcional · Ej. 3001234567" bind:value={formPhone} />
                </label>
                <label class="field-label">
                  Método de Pago Acordado *
                  <select class="select" bind:value={formPaymentMethod}>
                    <option value="Efectivo">Efectivo</option>
                    <option value="QR">QR</option>
                    <option value="Transferencia">Transferencia</option>
                    <option value="Tarjeta">Tarjeta</option>
                    <option value="Otro">Otro</option>
                  </select>
                </label>
                <label class="field-label">
                  Tope de Crédito *
                  <input class="input" type="number" min="0" step="1000" bind:value={formCreditLimit} />
                </label>
                <label class="field-label" style="grid-column: span 2;">
                  Descripción / Referencia (Opcional)
                  <input
                    class="input"
                    placeholder="Opcional · Ej. Torre 2 Apto 18502"
                    bind:value={formDescription}
                  />
                </label>
              </div>
            {/if}

            <div style="margin-top: 12px;">
              <label class="field-label">
                Nota de esta anotación (Opcional)
                <input
                  class="input"
                  placeholder="Ej. Retira pedido semanal"
                  bind:value={formMoveNote}
                />
              </label>
            </div>

            <!-- Live Credit Limit Validation Gauge -->
            <div
              style="margin-top: 16px; padding: 14px; border-radius: 10px; background: var(--bg-primary); border: 1px solid {exceedsProjectedLimit
                ? 'var(--accent-red)'
                : 'var(--border)'};"
            >
              <div style="display: flex; justify-content: space-between; font-size: 13px;">
                <span>Saldo actual: <strong>${currentAccountBalance.toLocaleString()}</strong></span>
                <span>Nuevo cargo: <strong>${cartTotal.toLocaleString()}</strong></span>
              </div>
              <div style="display: flex; justify-content: space-between; font-size: 14px; margin-top: 6px;">
                <span>
                  Total en cuenta:
                  <strong style="color: {exceedsProjectedLimit ? 'var(--accent-red)' : 'var(--accent-green)'};">
                    ${projectedBalance.toLocaleString()}
                  </strong>
                </span>
                <span>Tope: <strong>${activeCreditLimit.toLocaleString()}</strong></span>
              </div>
              <div class="bar-track">
                <div
                  class="bar-fill"
                  style="width: {limitUsagePct}%; background: {exceedsProjectedLimit
                    ? 'var(--accent-red)'
                    : limitUsagePct > 80
                      ? 'var(--accent-amber)'
                      : 'linear-gradient(90deg, #38bdf8, #34d399)'};"
                ></div>
              </div>

              {#if exceedsProjectedLimit}
                <div style="margin-top: 10px; font-size: 12.5px; color: var(--accent-red);">
                  ⚠ Atención: Este cargo supera el tope configurado para el cliente.
                  <label style="display: flex; align-items: center; gap: 8px; margin-top: 6px; color: #fecaca;">
                    <input type="checkbox" bind:checked={allowOverLimitConfirm} />
                    Autorizo guardar superando el tope de crédito
                  </label>
                </div>
              {/if}
            </div>

            <button
              class="btn btn-green"
              style="width: 100%; margin-top: 16px; padding: 14px; font-size: 15px;"
              disabled={loading}
              onclick={handleSaveAccountAndItems}
            >
              {loading
                ? 'Guardando y Sincronizando...'
                : accountActionMode === 'new'
                  ? 'Guardar Cuenta y Artículos (Sincronizar con Drive)'
                  : 'Anotar Artículos a la Cuenta (Sincronizar con Drive)'}
            </button>
          </div>

          <!-- Right Card: Quick Punctual Value + Barcode Scanner + Live Aronium Product Search + Cart -->
          <div class="card">
            <!-- Quick Punctual Value Annotator ABOVE Barcode Scanner (Dulces, comida, tinto / sin código) -->
            <div class="quick-value-box">
              <div style="font-size: 13.5px; font-weight: 800; color: #fde68a; margin-bottom: 8px;">
                Agregar Valor Puntual (Dulces, Tinto, Comida / Sin Código)
              </div>
              <div style="display: grid; grid-template-columns: 1fr; gap: 8px;">
                <label class="field-label" style="margin-bottom: 0;">
                  Descripción opcional (referente del artículo)
                  <input
                    class="input"
                    placeholder="Opcional · Ej. Dulces, tinto, empanada, almuerzo..."
                    bind:value={quickValueDesc}
                  />
                </label>
                <div class="quick-stepper">
                  <button
                    type="button"
                    class="btn btn-outline"
                    style="padding: 8px 12px; font-weight: 800;"
                    onclick={() => adjustQuickValue(-100)}
                  >
                    - 100
                  </button>
                  <input
                    class="input"
                    type="number"
                    min="0"
                    step="100"
                    style="max-width: 140px; font-weight: 800; text-align: center;"
                    placeholder="Valor $"
                    bind:value={quickValueAmount}
                  />
                  <button
                    type="button"
                    class="btn btn-outline"
                    style="padding: 8px 12px; font-weight: 800;"
                    onclick={() => adjustQuickValue(100)}
                  >
                    + 100
                  </button>
                  <button
                    type="button"
                    class="btn btn-amber"
                    style="padding: 8px 14px;"
                    onclick={() => addQuickValueToCart(quickValueAmount)}
                  >
                    + Anotar Valor
                  </button>
                </div>
                <div class="quick-presets">
                  <span style="font-size: 12px; color: var(--text-muted); font-weight: 600;">Valores rápidos:</span>
                  <button type="button" class="btn-preset" onclick={() => addQuickValueToCart(500)}>+$500</button>
                  <button type="button" class="btn-preset" onclick={() => addQuickValueToCart(1000)}>+$1.000</button>
                  <button type="button" class="btn-preset" onclick={() => addQuickValueToCart(2000)}>+$2.000</button>
                  <button type="button" class="btn-preset" onclick={() => addQuickValueToCart(5000)}>+$5.000</button>
                </div>
              </div>
            </div>

            <div class="card-title">
              <span>Agregar Artículos con Escáner o Buscador (Opcional)</span>
              <button
                class="btn btn-outline"
                style="padding: 5px 10px; font-size: 12px;"
                onclick={() => scannerInputEl?.focus()}
              >
                Enfocar Lector
              </button>
            </div>

            <div class="scanner-box">
              <div style="font-size: 12.5px; color: #a7f3d0; margin-bottom: 8px; font-weight: 600;">
                Pasa el lector de código de barras aquí (o escribe el nombre del producto):
              </div>
              <input
                bind:this={scannerInputEl}
                class="scanner-input"
                placeholder="Escanea código de barras + Enter, o escribe nombre..."
                bind:value={scannerQuery}
                oninput={() => searchCatalog(scannerQuery)}
                onkeydown={handleScannerKeydown}
              />

              {#if doctor?.pos_db_readable}
                <div class="catalog-results">
                  {#if catalogSearching && catalogMatches.length === 0}
                    <div style="padding: 12px; color: var(--text-muted); font-size: 13px;">
                      Buscando productos en Aronium...
                    </div>
                  {:else if catalogMatches.length === 0}
                    <div style="padding: 12px; color: var(--text-muted); font-size: 13px;">
                      No se encontraron productos con "{scannerQuery}".
                    </div>
                  {:else}
                    {#each catalogMatches.slice(0, 8) as prod}
                      <div
                        class="catalog-row"
                        role="button"
                        tabindex="0"
                        onclick={async () => {
                          addProductToCart(prod);
                          scannerQuery = '';
                          await searchCatalog('');
                          await tick();
                          scannerInputEl?.focus();
                        }}
                        onkeydown={async (e) => {
                          if (e.key === 'Enter') {
                            addProductToCart(prod);
                            scannerQuery = '';
                            await searchCatalog('');
                            await tick();
                            scannerInputEl?.focus();
                          }
                        }}
                      >
                        <div>
                          <strong>{prod.name}</strong>
                          <div style="font-size: 11.5px; color: var(--text-muted);">
                            Código: {prod.barcode || prod.code || 'Sin código'} · Stock Neto: {prod.net_stock_quantity}
                          </div>
                        </div>
                        <div style="text-align: right;">
                          <strong style="color: var(--accent-green);">
                            ${Math.round(prod.price).toLocaleString()}
                          </strong>
                          <div style="font-size: 11px; color: var(--accent-blue);">+ Agregar</div>
                        </div>
                      </div>
                    {/each}
                  {/if}
                </div>
              {/if}
            </div>

            <!-- Selected Items Snapshot Table -->
            <div style="font-size: 13px; font-weight: 700; margin-bottom: 8px; color: var(--text-muted);">
              Artículos Anotados en esta Operación ({cartItems.length}) — Precios Congelados
            </div>

            {#if cartItems.length === 0}
              <div
                style="padding: 20px; text-align: center; border: 1px dashed var(--border); border-radius: 10px; color: var(--text-muted); font-size: 13px;"
              >
                Todavía no has agregado artículos. Puedes crear la cuenta sin artículos, o escanear productos arriba.
              </div>
            {:else}
              <table>
                <thead>
                  <tr>
                    <th>Artículo</th>
                    <th>Precio</th>
                    <th>Cantidad</th>
                    <th>Subtotal</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {#each cartItems as item, idx}
                    <tr>
                      <td>
                        <strong>{item.nombre_snapshot}</strong>
                        <div style="font-size: 11px; color: var(--text-muted);">
                          {item.codigo_barras || 'SKU'}
                        </div>
                      </td>
                      <td>
                        <div>${item.precio_unitario_snapshot.toLocaleString()}</div>
                        {#if item.codigo_barras === 'PUNTUAL'}
                          <div style="display: flex; gap: 4px; margin-top: 4px;">
                            <button
                              class="btn btn-outline"
                              style="padding: 1px 6px; font-size: 10.5px;"
                              onclick={() => adjustCartItemUnitPrice(idx, -100)}
                            >
                              -100
                            </button>
                            <button
                              class="btn btn-outline"
                              style="padding: 1px 6px; font-size: 10.5px;"
                              onclick={() => adjustCartItemUnitPrice(idx, 100)}
                            >
                              +100
                            </button>
                          </div>
                        {/if}
                      </td>
                      <td>
                        <div style="display: flex; align-items: center; gap: 6px;">
                          <button
                            class="btn btn-outline"
                            style="padding: 3px 9px;"
                            onclick={() => updateItemQty(idx, -1)}
                          >
                            -
                          </button>
                          <strong>{item.cantidad}</strong>
                          <button
                            class="btn btn-outline"
                            style="padding: 3px 9px;"
                            onclick={() => updateItemQty(idx, 1)}
                          >
                            +
                          </button>
                        </div>
                      </td>
                      <td>
                        <strong>
                          ${Math.round(item.cantidad * item.precio_unitario_snapshot).toLocaleString()}
                        </strong>
                      </td>
                      <td>
                        <button
                          class="btn btn-red"
                          style="padding: 4px 8px; font-size: 11px;"
                          onclick={() => (cartItems = cartItems.filter((_, i) => i !== idx))}
                        >
                          Quitar
                        </button>
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {/if}
          </div>
        </div>
      {/if}

      <!-- BASIC TAB 2: VIEW ACCOUNTS & RECORD PAYMENTS (ABONOS) -->
      {#if basicTab === 'accounts'}
        <div class="kpi-grid">
          <div class="kpi-card">
            <div class="kpi-label">Total por Cobrar en Cuentas</div>
            <div class="kpi-value" style="color: var(--accent-amber);">
              ${(accountsReport?.total_saldo_pendiente || 0).toLocaleString()}
            </div>
            <div class="kpi-sub">
              Separado del cierre diario de caja de Aronium
            </div>
          </div>
          <div class="kpi-card">
            <div class="kpi-label">Clientes con Saldo Activo</div>
            <div class="kpi-value">{accountsReport?.cuentas_activas_con_saldo || 0}</div>
            <div class="kpi-sub">
              De {accountsReport?.total_cuentas || 0} cuentas registradas en cuentas.db
            </div>
          </div>
        </div>

        <!-- Inline Payment Form showing all accounts sharing the same Apartment/Location ID -->
        {#if activePayAccount}
          <div class="card" style="border-color: var(--accent-green);">
            <div class="card-title">
              <span>Registrar Pago / Abono — ID: <code>{activePayAccount.id}</code></span>
              <button class="btn btn-outline" onclick={() => (activePayAccount = null)}>Cancelar</button>
            </div>

            {#if sharedAccountsForPayment.length > 1}
              <div style="margin-bottom: 12px;">
                <div style="font-size: 13px; color: #fde68a; font-weight: 700; margin-bottom: 8px;">
                  Este ID ({activePayAccount.id}) tiene {sharedAccountsForPayment.length} cuentas registradas. Elige a cuál deseas abonar:
                </div>
                <div class="shared-accounts-grid">
                  {#each sharedAccountsForPayment as candidate}
                    <button
                      type="button"
                      class="shared-account-card {accKeyOf(candidate) === accKeyOf(activePayAccount) ? 'selected' : ''}"
                      onclick={() => {
                        activePayAccount = candidate;
                        payAmount = candidate.saldo_actual;
                        payMethod = candidate.metodo_pago || 'Efectivo';
                      }}
                    >
                      <div style="display: flex; justify-content: space-between; align-items: center; gap: 8px;">
                        <strong style="font-size: 14.5px;">{candidate.nombre}</strong>
                        <span class={accKeyOf(candidate) === accKeyOf(activePayAccount) ? 'badge badge-ok' : 'badge badge-warn'}>
                          ${candidate.saldo_actual.toLocaleString()}
                        </span>
                      </div>
                      <div style="font-size: 11.5px; color: var(--text-muted); margin-top: 4px;">
                        Método: {candidate.metodo_pago} {candidate.descripcion ? `· ${candidate.descripcion}` : ''}
                      </div>
                    </button>
                  {/each}
                </div>
              </div>
            {/if}

            <p style="font-size: 13px; color: var(--text-muted); margin-top: 0;">
              Cuenta seleccionada: <strong>{activePayAccount.nombre}</strong> ({activePayAccount.id}) · Saldo pendiente:
              <strong>${activePayAccount.saldo_actual.toLocaleString()}</strong>.
              Este pago se registra en <code>cuentas.db</code> sin dañar tu cierre diario de caja en Aronium.
            </p>
            <div class="form-grid">
              <label class="field-label">
                Monto que Paga el Cliente
                <input class="input" type="number" min="1" bind:value={payAmount} />
              </label>
              <label class="field-label">
                Método de Pago Recibido
                <select class="select" bind:value={payMethod}>
                  <option value="Efectivo">Efectivo</option>
                  <option value="QR">QR</option>
                  <option value="Transferencia">Transferencia</option>
                  <option value="Tarjeta">Tarjeta</option>
                  <option value="Otro">Otro</option>
                </select>
              </label>
              <label class="field-label">
                Nota del Pago
                <input class="input" bind:value={payNote} />
              </label>
            </div>
            <button
              class="btn btn-green"
              style="margin-top: 14px;"
              disabled={loading}
              onclick={handleRecordPayment}
            >
              Confirmar Pago para {activePayAccount.nombre} y Actualizar Google Drive
            </button>
          </div>
        {/if}

        {#if selectedStatement}
          <div class="card">
            <div class="card-title">
              <span>Recibo / Estado de Cuenta: {selectedStatementCustomer}</span>
              <button class="btn btn-outline" onclick={() => (selectedStatement = '')}>Cerrar</button>
            </div>
            <pre class="stmt-box">{selectedStatement}</pre>
          </div>
        {/if}

        <div class="card">
          <div class="card-title">
            <span>Cuentas de Clientes Registradas</span>
            <input
              class="input"
              style="max-width: 300px;"
              placeholder="Buscar por nombre, ID o teléfono..."
              bind:value={accountSearch}
            />
          </div>

          {#if filteredAccounts().length === 0}
            <div style="padding: 24px; text-align: center; color: var(--text-muted);">
              No hay cuentas registradas todavía. Ve a <strong>"1. Crear Cuenta / Anotar Artículos"</strong> para crear la primera.
            </div>
          {:else}
            {#each filteredAccounts() as acc}
              <div
                style="border: 1px solid var(--border); border-radius: 12px; padding: 16px; margin-bottom: 14px; background: var(--bg-secondary);"
              >
                <div style="display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap;">
                  <div>
                    <strong style="font-size: 16.5px;">{acc.nombre}</strong>
                    <span class={statusBadgeClass(acc.estado)} style="margin-left: 8px;">{acc.estado}</span>
                    {#if (acc.shared_id_count || 1) > 1}
                      <span class="badge badge-warn" style="margin-left: 6px;">
                        {acc.shared_id_count} cuentas en ID {acc.id}
                      </span>
                    {/if}
                    <div style="font-size: 12.5px; color: var(--text-muted); margin-top: 4px;">
                      ID: <strong>{acc.id}</strong> · Tel: {acc.telefono || 'Opcional'} · Pago: {acc.metodo_pago}{acc.descripcion ? ` · ${acc.descripcion}` : ''}
                    </div>
                  </div>
                  <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                    <button
                      class="btn btn-green"
                      onclick={() => {
                        activePayAccount = acc;
                        payAmount = acc.saldo_actual;
                        payMethod = acc.metodo_pago || 'Efectivo';
                      }}
                    >
                      Registrar Pago / Abono
                    </button>
                    <button
                      class="btn"
                      onclick={async () => {
                        selectExistingAccountForCharge(acc);
                        basicTab = 'create';
                        await tick();
                        scannerInputEl?.focus();
                      }}
                    >
                      Anotar Artículos
                    </button>
                    <button
                      class="btn btn-outline"
                      onclick={() =>
                        (expandedAccountKey = expandedAccountKey === accKeyOf(acc) ? '' : accKeyOf(acc))}
                    >
                      {expandedAccountKey === accKeyOf(acc) ? 'Ocultar Detalle' : 'Ver Detalle'}
                    </button>
                    <button
                      class="btn btn-outline"
                      onclick={() => handleViewStatement(accKeyOf(acc), acc.nombre)}
                    >
                      Recibo
                    </button>
                  </div>
                </div>

                <div style="display: flex; gap: 24px; margin-top: 12px; font-size: 13.5px; flex-wrap: wrap;">
                  <span>Total Cargos: <strong>${acc.total_cargos.toLocaleString()}</strong></span>
                  <span>Total Pagado: <strong>${acc.total_abonos.toLocaleString()}</strong></span>
                  <span style="color: var(--accent-amber);">
                    Saldo Pendiente: <strong>${acc.saldo_actual.toLocaleString()}</strong>
                  </span>
                  <span>
                    Tope: ${acc.tope_credito.toLocaleString()} (Disponible: ${acc.credito_disponible.toLocaleString()})
                  </span>
                </div>

                {#if expandedAccountKey === accKeyOf(acc) && acc.movimientos}
                  <table style="margin-top: 12px;">
                    <thead>
                      <tr>
                        <th>Fecha</th>
                        <th>Tipo</th>
                        <th>Monto</th>
                        <th>Método</th>
                        <th>Detalle / Artículos Congelados</th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each acc.movimientos as mv}
                        <tr>
                          <td>{mv.fecha}</td>
                          <td>
                            <span class={mv.tipo === 'ABONO' ? 'badge badge-ok' : 'badge badge-warn'}>
                              {mv.tipo}
                            </span>
                          </td>
                          <td><strong>${mv.monto.toLocaleString()}</strong></td>
                          <td>{mv.metodo_pago_usado}</td>
                          <td>
                            <div>{mv.nota}</div>
                            {#if mv.items && mv.items.length > 0}
                              <div style="font-size: 11.5px; color: var(--text-muted); margin-top: 4px;">
                                {#each mv.items as it}
                                  <div>
                                    • {it.cantidad}x {it.nombre_snapshot} ({it.codigo_barras || 'SKU'}) @ $
                                    {it.precio_unitario_snapshot.toLocaleString()} = $
                                    {it.subtotal.toLocaleString()}
                                  </div>
                                {/each}
                              </div>
                            {/if}
                          </td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                {/if}
              </div>
            {/each}
          {/if}
        </div>
      {/if}

      <!-- BASIC TAB 3: NET INVENTORY (STOCK ARONIUM - UNIDADES EN CUENTAS) -->
      {#if basicTab === 'inventory'}
        <div class="card">
          <div class="card-title">
            <span>Inventario Real Disponible (Stock Aronium - Artículos en Cuentas)</span>
            <div style="display: flex; gap: 10px;">
              <select class="select" style="width: 165px;" bind:value={invStatusFilter}>
                <option value="ALL">Todos</option>
                <option value="OK">Disponibles</option>
                <option value="STOCK_BAJO">Stock Bajo</option>
                <option value="AGOTADO">Agotados</option>
                <option value="STOCK_NEGATIVO">Stock Negativo</option>
              </select>
              <input
                class="input"
                style="width: 240px;"
                placeholder="Buscar producto o código..."
                bind:value={invSearch}
              />
            </div>
          </div>

          {#if !doctor?.pos_db_readable}
            <div style="padding: 20px; color: var(--text-muted);">
              Conecta tu archivo <code>pos.db</code> de Aronium en el botón <strong>"Seleccionar Base de Datos"</strong> arriba para ver tus productos.
            </div>
          {:else}
            <table>
              <thead>
                <tr>
                  <th>Código</th>
                  <th>Producto</th>
                  <th>Precio Venta</th>
                  <th>Stock Aronium</th>
                  <th>En Cuentas</th>
                  <th>Stock Neto Real</th>
                  <th>Estado</th>
                </tr>
              </thead>
              <tbody>
                {#each filteredInventory() as item}
                  <tr>
                    <td><code>{item.barcode || item.code}</code></td>
                    <td><strong>{item.name}</strong></td>
                    <td>${Math.round(item.sale_price).toLocaleString()}</td>
                    <td>{item.aronium_stock} {item.measurement_unit}</td>
                    <td>{item.committed_in_accounts_qty}</td>
                    <td><strong>{item.net_stock_quantity} {item.measurement_unit}</strong></td>
                    <td>
                      <span class={statusBadgeClass(item.status)}>
                        {friendlyStockStatus(item.status)}
                      </span>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </div>
      {/if}

      <!-- BASIC TAB 4: GOOGLE DRIVE & DATABASE SELECTOR -->
      {#if basicTab === 'setup'}
        <div class="panel-grid">
          <div class="card">
            <div class="card-title">
              <span>Guardar Mis Cuentas e Inventario en Google Drive</span>
              <span class={driveStatus?.connected ? 'badge badge-ok' : 'badge badge-warn'}>
                {driveStatus?.connected ? 'Conectado' : 'Modo Carpeta Local'}
              </span>
            </div>
            <p style="font-size: 13.5px; color: var(--text-muted); line-height: 1.55;">
              Cada vez que creas una cuenta, anotas artículos o registras un pago, el sistema genera un Excel
              actualizado (<code>{driveStatus?.master_workbook_name}</code>) y una copia segura de tu base de
              cuentas en Google Drive.
            </p>
            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
              <button class="btn" disabled={loading} onclick={handleDriveWebLogin}>
                Conectar Cuenta de Google Drive
              </button>
              <button class="btn btn-green" disabled={loading} onclick={handleSyncDrive}>
                Sincronizar con Drive Ahora
              </button>
            </div>
            {#if lastSyncResult}
              <div style="margin-top: 14px; font-size: 12.5px; color: var(--accent-green);">
                ✓ Archivo actualizado: <code>{lastSyncResult.master_workbook_path}</code>
              </div>
            {/if}
          </div>

          <div class="card">
            <div class="card-title">Elegir Bases de Datos (Aronium y Cuentas)</div>
            <p style="font-size: 13px; color: var(--text-muted); line-height: 1.5;">
              <strong>Productos de Aronium:</strong> <code>{configInfo?.config?.pos_db_path}</code><br />
              <strong>Base de Cuentas:</strong> <code>{configInfo?.config?.accounts_db_path}</code>
            </p>
            <button class="btn btn-outline" onclick={() => (showDbModal = true)}>
              Cambiar Ubicación de Bases de Datos
            </button>
          </div>
        </div>
      {/if}
    </div>

  <!-- ========================================================================= -->
  <!-- MODO AVANZADO (VISTA TÉCNICA Y ADMINISTRATIVA COMPLETA)                   -->
  <!-- ========================================================================= -->
  {:else}
    <div class="layout">
      <aside class="sidebar">
        <div>
          <button
            class="nav-btn {advTab === 'dashboard' ? 'active' : ''}"
            onclick={() => (advTab = 'dashboard')}
          >
            <span>1. Dashboard & Métricas</span>
            <span>📊</span>
          </button>

          <button
            class="nav-btn {advTab === 'accounts' ? 'active' : ''}"
            onclick={() => (advTab = 'accounts')}
          >
            <span>2. Ledger Cuentas (cuentas.db)</span>
            <span>🧾</span>
          </button>

          <button
            class="nav-btn {advTab === 'inventory' ? 'active' : ''}"
            onclick={() => {
              advTab = 'inventory';
              loadKardex();
            }}
          >
            <span>3. Inventario Neto & Kardex</span>
            <span>📦</span>
          </button>

          <button
            class="nav-btn {advTab === 'registers' ? 'active' : ''}"
            onclick={() => (advTab = 'registers')}
          >
            <span>4. Cajas Registradoras</span>
            <span>🖥️</span>
          </button>

          <button
            class="nav-btn {advTab === 'drive' ? 'active' : ''}"
            onclick={() => (advTab = 'drive')}
          >
            <span>5. Google Drive & Backups</span>
            <span>☁️</span>
          </button>

          <button
            class="nav-btn {advTab === 'doctor' ? 'active' : ''}"
            onclick={() => {
              advTab = 'doctor';
              if (!updateCheck) handleCheckForUpdate(true);
            }}
          >
            <span>6. Diagnóstico & Actualización</span>
            <span>🛡️</span>
          </button>
        </div>

        <div class="card" style="margin-bottom: 0; padding: 14px;">
          <div style="font-size: 11px; color: var(--text-muted); margin-bottom: 6px;">
            MOTOR SIDECAR GO CLI
          </div>
          <div style="font-size: 12.5px; font-weight: 700; color: var(--accent-green);">
            ● {doctor?.pos_db_readable ? 'pos.db Conectado (RO)' : 'pos.db Pendiente'}
          </div>
          <div style="font-size: 11.5px; color: var(--text-muted); margin-top: 4px;">
            Caja: <strong>{doctor?.cash_register_id || 'POS-01'}</strong>
          </div>
          <button
            class="btn btn-green"
            style="width: 100%; margin-top: 10px;"
            disabled={loading}
            onclick={handleSyncDrive}
          >
            Sincronizar Drive Ahora
          </button>
        </div>
      </aside>

      <main class="main-area">
        <div class="topbar">
          <div>
            <h1 style="margin: 0; font-size: 20px;">
              {snapshot?.store_name || 'Aronium Utility'} — Panel Técnico Avanzado
            </h1>
            <div style="font-size: 12px; color: var(--text-muted); margin-top: 4px;">
              Doble Base: <code>pos.db</code> (Solo Lectura) + <code>cuentas.db</code> (SQLite WAL)
            </div>
          </div>
          <div style="display: flex; gap: 10px; flex-wrap: wrap;">
            <button
              class="btn btn-outline"
              disabled={checkingUpdate || applyingUpdate}
              onclick={() => {
                advTab = 'doctor';
                handleCheckForUpdate(false);
              }}
            >
              {checkingUpdate ? 'Verificando GitHub...' : 'Verificar Actualización GitHub'}
            </button>
            <button class="btn btn-outline" disabled={loading} onclick={refreshAll}>
              {loading ? 'Actualizando...' : 'Actualizar Datos'}
            </button>
            <button class="btn" disabled={loading} onclick={handleSyncDrive}>
              Sincronizar Drive + Backup
            </button>
          </div>
        </div>

        {#if advTab === 'dashboard'}
          <div class="kpi-grid">
            <div class="kpi-card">
              <div class="kpi-label">Ventas POS en Aronium (pos.db)</div>
              <div class="kpi-value">${(dailyReport?.total_sales || 0).toLocaleString()}</div>
              <div class="kpi-sub">
                {dailyReport?.total_transactions || 0} transacciones · Sin ruido de cartera
              </div>
            </div>

            <div class="kpi-card">
              <div class="kpi-label">Saldo Cartera (cuentas.db)</div>
              <div class="kpi-value" style="color: var(--accent-amber);">
                ${(accountsReport?.total_saldo_pendiente || 0).toLocaleString()}
              </div>
              <div class="kpi-sub">
                {accountsReport?.cuentas_activas_con_saldo || 0} activas · {accountsReport?.cuentas_sobre_tope || 0} sobre tope
              </div>
            </div>

            <div class="kpi-card">
              <div class="kpi-label">Valorización Inventario Neto</div>
              <div class="kpi-value" style="color: var(--accent-green);">
                ${(snapshot?.inventory_summary?.total_retail_value || 0).toLocaleString()}
              </div>
              <div class="kpi-sub">
                Costo: ${(snapshot?.inventory_summary?.total_cost_value || 0).toLocaleString()}
              </div>
            </div>

            <div class="kpi-card">
              <div class="kpi-label">Alertas Stock Neto</div>
              <div class="kpi-value" style="color: var(--accent-red);">
                {(snapshot?.inventory_summary?.low_stock_count || 0) +
                  (snapshot?.inventory_summary?.out_of_stock_count || 0) +
                  (snapshot?.inventory_summary?.negative_stock_count || 0)}
              </div>
              <div class="kpi-sub">
                Bajo: {snapshot?.inventory_summary?.low_stock_count || 0} · Agotados: {snapshot?.inventory_summary?.out_of_stock_count || 0}
              </div>
            </div>
          </div>

          <div class="panel-grid">
            <div class="card">
              <h3 class="card-title">Ventas por Caja Registradora (pos.db)</h3>
              {#each snapshot?.sales_by_register || [] as reg}
                <div style="margin-bottom: 12px;">
                  <div style="display: flex; justify-content: space-between; font-size: 13px;">
                    <strong>{reg.register_id} ({reg.transaction_count} docs)</strong>
                    <span><strong>${reg.total_sales.toLocaleString()}</strong></span>
                  </div>
                </div>
              {/each}
            </div>

            <div class="card">
              <h3 class="card-title">Top Productos Vendidos</h3>
              <table>
                <thead>
                  <tr><th>Producto</th><th>Cant.</th><th>Total</th></tr>
                </thead>
                <tbody>
                  {#each dailyReport?.top_products || [] as prod}
                    <tr>
                      <td><strong>{prod.product_name}</strong></td>
                      <td>{prod.quantity}</td>
                      <td>${prod.revenue.toLocaleString()}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </div>
        {/if}

        {#if advTab === 'accounts'}
          <div class="card">
            <div class="card-title">
              <span>Libro Mayor de Cuentas (<code>{accountsReport?.accounts_db_path}</code>)</span>
              <button class="btn btn-green" onclick={() => { setUIMode('basic'); basicTab = 'create'; }}>
                Abrir Creador + Escáner
              </button>
            </div>
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Cliente</th>
                  <th>Teléfono</th>
                  <th>Cargos</th>
                  <th>Abonos</th>
                  <th>Saldo Actual</th>
                  <th>Tope</th>
                  <th>Estado</th>
                </tr>
              </thead>
              <tbody>
                {#each accountsReport?.cuentas || [] as acc}
                  <tr>
                    <td><code>{acc.id}</code></td>
                    <td><strong>{acc.nombre}</strong></td>
                    <td>{acc.telefono}</td>
                    <td>${acc.total_cargos.toLocaleString()}</td>
                    <td>${acc.total_abonos.toLocaleString()}</td>
                    <td><strong>${acc.saldo_actual.toLocaleString()}</strong></td>
                    <td>${acc.tope_credito.toLocaleString()}</td>
                    <td><span class={statusBadgeClass(acc.estado)}>{acc.estado}</span></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}

        {#if advTab === 'inventory'}
          <div class="card">
            <div class="card-title">
              <div style="display: flex; gap: 10px;">
                <button
                  class="btn {invSubView === 'catalog' ? '' : 'btn-outline'}"
                  onclick={() => (invSubView = 'catalog')}
                >
                  Catálogo & Inventario Neto
                </button>
                <button
                  class="btn {invSubView === 'kardex' ? '' : 'btn-outline'}"
                  onclick={() => { invSubView = 'kardex'; loadKardex(); }}
                >
                  Kardex de Movimientos
                </button>
              </div>
              <input class="input" style="width: 240px;" placeholder="Buscar producto..." bind:value={invSearch} />
            </div>

            {#if invSubView === 'catalog'}
              <table>
                <thead>
                  <tr>
                    <th>SKU</th><th>Código</th><th>Producto</th><th>Stock POS</th><th>En Cuentas</th><th>Stock Neto</th><th>Costo</th><th>Venta</th><th>Estado</th>
                  </tr>
                </thead>
                <tbody>
                  {#each filteredInventory() as item}
                    <tr>
                      <td><code>{item.code}</code></td>
                      <td><code>{item.barcode || '-'}</code></td>
                      <td><strong>{item.name}</strong></td>
                      <td>{item.aronium_stock}</td>
                      <td>{item.committed_in_accounts_qty}</td>
                      <td><strong>{item.net_stock_quantity}</strong></td>
                      <td>${item.cost.toLocaleString()}</td>
                      <td>${item.sale_price.toLocaleString()}</td>
                      <td><span class={statusBadgeClass(item.status)}>{item.status}</span></td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {:else}
              <table>
                <thead>
                  <tr><th>Fecha</th><th>Doc</th><th>Caja</th><th>Producto</th><th>Delta</th><th>Resultante</th></tr>
                </thead>
                <tbody>
                  {#each kardexList as k}
                    <tr>
                      <td>{k.recorded_at}</td>
                      <td>{k.document_number}</td>
                      <td>{k.register_id}</td>
                      <td>{k.product_name}</td>
                      <td>{k.delta_qty}</td>
                      <td>{k.resulting_qty}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {/if}
          </div>
        {/if}

        {#if advTab === 'registers'}
          <div class="card">
            <h3 class="card-title">Trazabilidad por Caja Registradora (PRO-FR-004)</h3>
            <table>
              <thead>
                <tr><th>ID Caja</th><th>Docs</th><th>Contado</th><th>Crédito POS</th><th>Total Ventas</th><th>Ticket Promedio</th></tr>
              </thead>
              <tbody>
                {#each snapshot?.sales_by_register || [] as reg}
                  <tr>
                    <td><strong>{reg.register_id}</strong></td>
                    <td>{reg.transaction_count}</td>
                    <td>${reg.cash_sales.toLocaleString()}</td>
                    <td>${reg.credit_sales.toLocaleString()}</td>
                    <td><strong>${reg.total_sales.toLocaleString()}</strong></td>
                    <td>${reg.average_ticket.toFixed(2)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}

        {#if advTab === 'drive'}
          <div class="panel-grid">
            <div class="card">
              <h3 class="card-title">Google Drive & Google Sheets (OAuth 2.0 Web)</h3>
              <p style="font-size: 13px; color: var(--text-muted);">
                Exporta 4 pestañas (<code>Cuentas</code>, <code>Movimientos</code>, <code>Detalle_Articulos</code>, <code>Inventario_Neto</code>) y respaldo binario en caliente <code>cuentas_backup.db</code>.
              </p>
              <div style="display: flex; gap: 10px;">
                <button class="btn" onclick={handleDriveWebLogin}>Autenticar Google Drive Web</button>
                <button class="btn btn-green" onclick={handleSyncDrive}>Sincronizar Ahora</button>
              </div>
            </div>
            <div class="card">
              <h3 class="card-title">Respaldo en Caliente AES-256-GCM (pos.db)</h3>
              <input class="input" type="password" placeholder="Frase AES-256-GCM opcional" bind:value={backupPassphrase} />
              <button class="btn" style="margin-top: 12px; width: 100%;" onclick={handleCreateBackup}>
                Crear Respaldo en Caliente
              </button>
              {#if lastBackup}
                <div style="margin-top: 10px; font-size: 12px;">
                  <code>{lastBackup.archive_path}</code>
                </div>
              {/if}
            </div>
          </div>
        {/if}

        {#if advTab === 'doctor'}
          <!-- GitHub Releases Direct Auto-Updater Card -->
          <div class="card" style="border-color: var(--accent-blue); margin-bottom: 16px;">
            <div class="card-title">
              <span>Actualización Directa desde GitHub Releases (<code>SilverFeit/Aronium-Utility</code>)</span>
              {#if updateCheck}
                <span class={updateCheck.update_available ? 'badge badge-warn' : 'badge badge-ok'}>
                  {updateCheck.update_available
                    ? `¡Nueva versión ${updateCheck.tag_name} disponible!`
                    : `✓ Al día (v${updateCheck.current_version})`}
                </span>
              {/if}
            </div>
            <p style="font-size: 13px; color: var(--text-muted); margin-top: 0; line-height: 1.5;">
              Consulta la última release oficial publicada en GitHub, descarga el instalador unificado para Windows,
              verifica su firma/hash SHA-256, cierra los procesos en uso y actualiza el sistema preservando intactas
              tus cuentas en <code>cuentas.db</code> y tu configuración.
            </p>

            {#if updateCheck}
              <div
                style="padding: 12px 14px; border-radius: 10px; background: var(--bg-primary); border: 1px solid var(--border); margin-bottom: 14px; font-size: 13px;"
              >
                <div style="display: flex; gap: 22px; flex-wrap: wrap;">
                  <span>Versión instalada: <strong>v{updateCheck.current_version}</strong></span>
                  <span>
                    Última release en GitHub:
                    <strong style="color: var(--accent-green);">
                      {updateCheck.tag_name || `v${updateCheck.latest_version}`}
                    </strong>
                  </span>
                  {#if updateCheck.installer_asset_name}
                    <span>Paquete: <code>{updateCheck.installer_asset_name}</code></span>
                  {/if}
                  {#if updateCheck.published_at}
                    <span>Publicado: {updateCheck.published_at}</span>
                  {/if}
                </div>
                {#if updateCheck.release_title}
                  <div style="margin-top: 6px; font-weight: 700; color: #bae6fd;">
                    {updateCheck.release_title}
                  </div>
                {/if}
                {#if updateCheck.release_notes}
                  <pre class="stmt-box" style="margin-top: 8px; max-height: 140px;">{updateCheck.release_notes}</pre>
                {/if}
              </div>
            {/if}

            {#if updateApplyResult}
              <div
                style="padding: 10px 14px; border-radius: 8px; background: rgba(16, 185, 129, 0.14); border: 1px solid var(--accent-green); color: #a7f3d0; font-size: 13px; margin-bottom: 12px;"
              >
                <strong>{updateApplyResult.message}</strong>
                {#if updateApplyResult.sha256}
                  <div style="font-size: 11.5px; margin-top: 4px;">
                    SHA-256: <code>{updateApplyResult.sha256}</code>
                    {updateApplyResult.verified_sha256 ? ' (✓ Verificado contra SHA256SUMS.txt)' : ''}
                  </div>
                {/if}
              </div>
            {/if}

            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
              <button
                class="btn"
                disabled={checkingUpdate || applyingUpdate}
                onclick={() => handleCheckForUpdate(false)}
              >
                {checkingUpdate ? 'Consultando GitHub Releases...' : '1. Verificar Última Release en GitHub'}
              </button>

              {#if updateCheck?.update_available}
                <button
                  class="btn btn-green"
                  disabled={applyingUpdate || checkingUpdate}
                  onclick={() => handleApplyUpdate(false)}
                >
                  {applyingUpdate
                    ? 'Descargando y Verificando Instalador...'
                    : `2. Descargar e Instalar ${updateCheck.tag_name} Ahora`}
                </button>
              {:else}
                <button
                  class="btn btn-outline"
                  disabled={applyingUpdate || checkingUpdate}
                  onclick={() => handleApplyUpdate(true)}
                >
                  {applyingUpdate
                    ? 'Descargando desde GitHub...'
                    : 'Reinstalar / Reparar desde Última Release de GitHub'}
                </button>
              {/if}
            </div>
          </div>

          <div class="panel-grid">
            <div class="card">
              <h3 class="card-title">Diagnóstico Técnico (`aronium-util doctor`)</h3>
              <table>
                <tbody>
                  <tr><td>pos.db (Solo Lectura)</td><td><code>{doctor?.pos_db_path}</code> ({doctor?.pos_db_readable ? 'OK' : 'No encontrado'})</td></tr>
                  <tr><td>cuentas.db (WAL)</td><td><code>{doctor?.accounts_db_path}</code> ({doctor?.accounts_db_ready ? 'Lista' : 'Error'})</td></tr>
                  <tr><td>PRAGMA quick_check</td><td>{doctor?.quick_check_result || 'N/A'}</td></tr>
                  <tr><td>Esquema Aronium</td><td>{doctor?.aronium_schema_version || 'N/A'}</td></tr>
                  <tr><td>Outbox Offline</td><td>{doctor?.pending_outbox_count || 0} pendientes</td></tr>
                </tbody>
              </table>
            </div>
            <div class="card">
              <h3 class="card-title">Inicio Automático en Windows</h3>
              <p style="font-size: 13px; color: var(--text-muted);">
                Estado: <code>{serviceStatus?.status}</code>
              </p>
              <button class="btn btn-green" style="width: 100%;" onclick={handleInstallAutoStartService}>
                Registrar Acceso Directo en Carpeta Startup
              </button>
            </div>
          </div>
        {/if}
      </main>
    </div>
  {/if}
</div>
