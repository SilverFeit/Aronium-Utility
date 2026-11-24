<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { clearDebugLogs, fetchDebugLogs, runAroniumCLI } from './lib/ipc';
  import type {
    AccountMovementItemInput,
    AccountsLedgerReport,
    AllDBsBackupResult,
    BackupManifest,
    CatalogProduct,
    CatalogSearchResponse,
    ConfigInfoResponse,
    CustomerAccount,
    DailyClosingReport,
    DashboardSnapshot,
    DebugLogsPayload,
    DoctorReport,
    DriveStatus,
    InventoryItem,
    KardexEntry,
    RepairReport,
    SyncResult,
    UpdateApplyResult,
    UpdateCheckResult,
  } from './lib/types';

  type UIMode = 'basic' | 'advanced';
  type BasicTab = 'create' | 'accounts' | 'inventory' | 'setup';
  type AdvTab = 'dashboard' | 'accounts' | 'inventory' | 'registers' | 'drive' | 'doctor' | 'logs';

  let uiMode = $state<UIMode>('basic');
  let basicTab = $state<BasicTab>('create');
  let advTab = $state<AdvTab>('dashboard');
  let loading = $state(false);
  let refreshing = $state(false);
  let savingAccount = $state(false);
  let recordingPayment = $state(false);
  let syncingDrive = $state(false);
  let backingUpAllDBs = $state(false);
  let loadingLogs = $state(false);
  let cliPingResult = $state('');
  let debugLogs = $state<DebugLogsPayload | null>(null);
  let lastAllDBsBackup = $state<AllDBsBackupResult | null>(null);
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
  let serviceStatus = $state<{
    status: string;
    log_path: string;
    autostart?: { enabled: boolean; preference: string; shortcut_path: string };
  } | null>(null);
  let updateCheck = $state<UpdateCheckResult | null>(null);
  let updateApplyResult = $state<UpdateApplyResult | null>(null);
  let checkingUpdate = $state(false);
  let applyingUpdate = $state(false);
  let confirmDeleteAccount = $state<CustomerAccount | null>(null);
  let deletingAccount = $state(false);

  // Database path selector inputs & v2.0.0 settings state
  let customPosDbPath = $state('');
  let customPosDbPassword = $state('');
  let clearPosDbPassword = $state(false);
  let customAccountsDbPath = $state('');

  // Optional live Drive sync toggle (off by default -> instant local writes)
  let autoSyncLive = $state(false);

  // 4:3 / 720p adaptive UI scaling, compact mode & theme ('dark' default | 'light')
  let uiScalePercent = $state(90);
  let uiCompactMode = $state(true);
  let uiTheme = $state<'dark' | 'light'>('dark');

  // Configurable Timezone (Local PC clock vs manual)
  let timezoneInput = $state('Local');
  let customTimezoneValue = $state('UTC-5');
  let effectiveTimezone = $state('');
  let currentLocalTime = $state('');

  // 5-digit numeric PIN protection for Modo Avanzado, Delete & Reset
  let hasAdvancedPin = $state(false);
  let advancedUnlocked = $state(false);
  let sessionPin = $state('');
  let pinModalVisible = $state(false);
  let pendingPinAction = $state<'enter_advanced' | 'delete_account' | 'reset_accounts_table' | null>(null);
  let pinPromptInput = $state('');
  let pinPromptError = $state('');
  let curPinInput = $state('');
  let newPinInput = $state('');
  let confirmNewPinInput = $state('');

  // System Repair & Accounts Table Reset
  let repairing = $state(false);
  let lastRepairReport = $state<RepairReport | null>(null);
  let showResetTableModal = $state(false);
  let resettingAccountsTable = $state(false);

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

  // Quick Punctual Value entry above scanner
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
    if (mode === 'advanced' && hasAdvancedPin && !advancedUnlocked) {
      pendingPinAction = 'enter_advanced';
      pinPromptInput = '';
      pinPromptError = '';
      pinModalVisible = true;
      return;
    }
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
    refreshing = true;
    try {
      const [cfgRes, docRes, accRes, drvRes, srvRes] = await Promise.all([
        runAroniumCLI<ConfigInfoResponse>(['config', 'get', '--json']),
        runAroniumCLI<DoctorReport>(['doctor', '--json']),
        runAroniumCLI<AccountsLedgerReport>(['accounts', 'list', '--json']),
        runAroniumCLI<DriveStatus>(['drive', 'status', '--json']),
        runAroniumCLI<{ status: string; log_path: string; autostart?: { enabled: boolean; preference: string; shortcut_path: string } }>(['service', 'status', '--json']),
      ]);

      configInfo = cfgRes;
      doctor = docRes;
      accountsReport = accRes;
      driveStatus = drvRes;
      serviceStatus = srvRes;

      autoSyncLive = Boolean(cfgRes.auto_sync_drive ?? cfgRes.config?.watch?.auto_sync_drive ?? false);
      uiScalePercent = cfgRes.ui_scale_percent ?? cfgRes.config?.ui?.scale_percent ?? 90;
      uiCompactMode = cfgRes.ui_compact_mode ?? cfgRes.config?.ui?.compact_mode ?? true;
      const loadedTheme = cfgRes.ui_theme ?? cfgRes.config?.ui?.theme;
      if (loadedTheme === 'light' || loadedTheme === 'dark') {
        uiTheme = loadedTheme;
      }
      effectiveTimezone = cfgRes.effective_timezone || '';
      currentLocalTime = cfgRes.current_local_time || '';
      hasAdvancedPin = Boolean(cfgRes.has_advanced_pin);
      if (!hasAdvancedPin) {
        advancedUnlocked = true;
      } else if (!advancedUnlocked && uiMode === 'advanced') {
        uiMode = 'basic';
      }

      const rawTz = cfgRes.config?.timezone || 'Local';
      const presetTzs = ['Local', 'America/Bogota', 'America/Lima', 'America/Mexico_City', 'America/Santiago', 'America/Buenos_Aires', 'UTC-5'];
      if (presetTzs.includes(rawTz)) {
        timezoneInput = rawTz;
      } else {
        timezoneInput = 'custom';
        customTimezoneValue = rawTz;
      }

      if (!customPosDbPath) customPosDbPath = cfgRes.config.pos_db_path;
      if (!customAccountsDbPath) customAccountsDbPath = cfgRes.config.accounts_db_path;

      if (docRes.pos_db_readable) {
        void searchCatalog(scannerQuery);
        void (async () => {
          try {
            snapshot = await runAroniumCLI<DashboardSnapshot>(['admin', '--json']);
          } catch {
            snapshot = null;
          }
          try {
            dailyReport = await runAroniumCLI<DailyClosingReport>(['reports', 'daily', '--deliver=false', '--json']);
          } catch {
            dailyReport = null;
          }
        })();
      } else {
        snapshot = null;
        dailyReport = null;
        catalogMatches = [];
      }
    } catch (e: any) {
      showToast(`Error comunicando con aronium-util: ${e?.message || e}`, true);
    } finally {
      refreshing = false;
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
          'Por favor completa las 2 partes del ID (Texto y Número, ej. ABC-123) y el Nombre completo. Teléfono y descripción son opcionales.',
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

    savingAccount = true;
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
        args.push(autoSyncLive ? '--sync=true' : '--sync=false');
        args.push('--json');
        await runAroniumCLI(args);
        showToast(
          autoSyncLive
            ? `¡Cuenta [${fullId}] "${formName.trim()}" guardada${cartItems.length > 0 ? ` con ${cartItems.length} concepto(s)` : ''} y sincronizada con Drive!`
            : `¡Cuenta [${fullId}] "${formName.trim()}" guardada en local${cartItems.length > 0 ? ` con ${cartItems.length} concepto(s)` : ''}!`
        );
      } else {
        if (cartItems.length === 0) {
          showToast('Escanea, agrega un artículo o ingresa un valor puntual para anotar en la cuenta.', true);
          savingAccount = false;
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
          autoSyncLive ? '--sync=true' : '--sync=false',
          '--json',
        ];
        await runAroniumCLI(args);
        showToast(
          autoSyncLive
            ? `¡Conceptos anotados a [${selectedExistingAccount?.id}] ${selectedExistingAccount?.nombre} y sincronizados con Drive!`
            : `¡Conceptos anotados al instante en [${selectedExistingAccount?.id}] ${selectedExistingAccount?.nombre}!`
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
      savingAccount = false;
    }
  }

  async function handleRecordPayment() {
    if (!activePayAccount || !payAmount || payAmount <= 0) {
      showToast('Ingresa un monto de pago válido mayor a 0.', true);
      return;
    }
    recordingPayment = true;
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
        autoSyncLive ? '--sync=true' : '--sync=false',
        '--json',
      ]);
      const paidCustomer = activePayAccount.nombre;
      const paidId = activePayAccount.id;
      activePayAccount = null;
      payAmount = 0;
      await refreshAll();
      showToast(
        autoSyncLive
          ? `¡Pago registrado para [${paidId}] ${paidCustomer} en cuentas.db y sincronizado con Drive!`
          : `¡Pago registrado en local para [${paidId}] ${paidCustomer} (el cierre de Aronium no fue alterado)!`
      );
    } catch (e: any) {
      showToast(`Error registrando pago: ${e?.message || e}`, true);
    } finally {
      recordingPayment = false;
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
      if (clearPosDbPassword) {
        args.push('--clear-pos-db-password');
      } else if (customPosDbPassword.trim()) {
        args.push('--pos-db-password', customPosDbPassword.trim());
      }
      if (createDemoPos) {
        args.push('--create-demo-pos');
      }
      args.push('--json');

      const res = await runAroniumCLI<{ config: any; doctor: DoctorReport }>(args);
      customPosDbPath = res.config.pos_db_path;
      customAccountsDbPath = res.config.accounts_db_path;
      clearPosDbPassword = false;
      customPosDbPassword = '';
      showDbModal = false;
      await refreshAll();
      if (res.doctor?.pos_db_readable) {
        showToast(
          `¡Bases conectadas! Se cargaron ${res.doctor.pos_db_product_count ?? 0} producto(s) desde pos.db.`
        );
      } else {
        showToast(
          'Se guardó la ruta, pero aún no encontramos o no se pudo leer pos.db en esa ubicación.',
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
    syncingDrive = true;
    try {
      lastSyncResult = await runAroniumCLI<SyncResult>(['sync', '--json']);
      await refreshAll();
      showToast(`Sincronización completa guardada en: ${lastSyncResult.master_workbook_path}`);
    } catch (e: any) {
      showToast(`Error sincronizando con Google Drive: ${e?.message || e}`, true);
    } finally {
      syncingDrive = false;
    }
  }

  async function handleBackupAllDBsToDrive() {
    backingUpAllDBs = true;
    try {
      const res = await runAroniumCLI<AllDBsBackupResult>(['backup', 'all-dbs', '--json']);
      lastAllDBsBackup = res;
      showToast(res.message);
    } catch (e: any) {
      showToast(`Error respaldando bases de datos (.db): ${e?.message || e}`, true);
    } finally {
      backingUpAllDBs = false;
    }
  }

  async function handleLoadDebugLogs() {
    loadingLogs = true;
    try {
      debugLogs = await fetchDebugLogs();
    } catch (e: any) {
      showToast(`Error cargando logs de debug: ${e?.message || e}`, true);
    } finally {
      loadingLogs = false;
    }
  }

  async function handleClearLogs() {
    loadingLogs = true;
    try {
      await clearDebugLogs();
      await handleLoadDebugLogs();
      showToast('Historial de logs de debug limpiado.');
    } catch (e: any) {
      showToast(`Error limpiando logs: ${e?.message || e}`, true);
    } finally {
      loadingLogs = false;
    }
  }

  async function handlePingCLI() {
    loadingLogs = true;
    const t0 = performance.now();
    try {
      const rep = await runAroniumCLI<DoctorReport>(['doctor', '--json']);
      const ms = Math.round(performance.now() - t0);
      cliPingResult = `✓ CLI respondió en ${ms} ms · pos.db=${rep.pos_db_readable ? 'OK' : 'Pendiente'} (${rep.pos_db_product_count ?? 0} productos) · cuentas.db=${rep.accounts_db_ready ? 'OK' : 'Error'} (${rep.accounts_count ?? 0} cuentas)`;
      await handleLoadDebugLogs();
    } catch (e: any) {
      const ms = Math.round(performance.now() - t0);
      cliPingResult = `✗ Error CLI (${ms} ms): ${e?.message || e}`;
      await handleLoadDebugLogs();
    } finally {
      loadingLogs = false;
    }
  }

  async function handleCopyLogs() {
    if (!debugLogs) return;
    const text = `=== INFO SIDECAR CLI ===\nVersión UI: ${debugLogs.ui_version || debugLogs.cli_version || 'N/A'}\nRuta CLI: ${debugLogs.cli_bin_path} (Existe: ${debugLogs.cli_bin_exists})\nAppData: ${debugLogs.app_data_dir}\n\n=== DEBUG LOG (${debugLogs.debug_log_path}) ===\n${debugLogs.debug_log}\n\n=== SERVICE LOG (${debugLogs.service_log_path}) ===\n${debugLogs.service_log}`;
    try {
      await navigator.clipboard.writeText(text);
      showToast('Logs de debug copiados al portapapeles.');
    } catch {
      showToast('Selecciona el texto de los logs manualmente para copiarlo.', true);
    }
  }

  async function handleDriveWebLogin(oneClick = true) {
    showToast(
      oneClick
        ? 'Abriendo Google en tu navegador para conectar en 1 clic...'
        : 'Abriendo asistente OAuth de Google Drive en tu navegador...'
    );
    try {
      const args = [
        'drive',
        'login',
        oneClick ? '--one-click=true' : '--one-click=false',
        '--timeout',
        '180',
        '--json',
      ];
      await runAroniumCLI(args);
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

  async function handleToggleAutostart(enableNext: boolean) {
    loading = true;
    try {
      const action = enableNext ? 'enable' : 'disable';
      const st = await runAroniumCLI<{ enabled: boolean; shortcut_path: string }>([
        'service',
        'autostart',
        action,
        '--json',
      ]);
      await refreshAll();
      if (st.enabled) {
        showToast('Inicio automático ACTIVADO: Aronium Utility se abrirá al encender el PC.');
      } else {
        showToast('Inicio automático DESACTIVADO.');
      }
    } catch (e: any) {
      showToast(`Error cambiando inicio automático: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleInstallAutoStartService() {
    await handleToggleAutostart(true);
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

  async function handleSavePrefs(overrides?: {
    autoSync?: boolean;
    scale?: number;
    compact?: boolean;
    theme?: 'dark' | 'light';
    tz?: string;
  }) {
    loading = true;
    try {
      const nextSync = overrides?.autoSync ?? autoSyncLive;
      const nextScale = overrides?.scale ?? uiScalePercent;
      const nextCompact = overrides?.compact ?? uiCompactMode;
      const nextTheme = overrides?.theme ?? uiTheme;
      const resolvedTZ =
        overrides?.tz ?? (timezoneInput === 'custom' ? customTimezoneValue.trim() || 'Local' : timezoneInput);

      const res = await runAroniumCLI<{
        effective_timezone: string;
        current_local_time: string;
        auto_sync_drive: boolean;
        ui_scale_percent: number;
        ui_compact_mode: boolean;
        ui_theme: 'dark' | 'light';
      }>([
        'config',
        'set-prefs',
        '--timezone',
        resolvedTZ,
        nextSync ? '--auto-sync-drive=true' : '--auto-sync-drive=false',
        '--ui-scale',
        String(nextScale),
        nextCompact ? '--ui-compact=true' : '--ui-compact=false',
        '--ui-theme',
        nextTheme,
        '--json',
      ]);

      autoSyncLive = res.auto_sync_drive;
      uiScalePercent = res.ui_scale_percent;
      uiCompactMode = res.ui_compact_mode;
      uiTheme = res.ui_theme === 'light' ? 'light' : 'dark';
      effectiveTimezone = res.effective_timezone;
      currentLocalTime = res.current_local_time;
      try {
        localStorage.setItem('aronium_ui_theme', uiTheme);
      } catch {
        // ignore storage errors
      }
      showToast(
        `Preferencias guardadas · Hora local: ${res.current_local_time} (${res.effective_timezone}) · Escala: ${res.ui_scale_percent}% · Tema: ${uiTheme === 'light' ? 'Claro' : 'Oscuro'}`
      );
    } catch (e: any) {
      showToast(`Error guardando preferencias: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  function handleToggleTheme() {
    const next: 'dark' | 'light' = uiTheme === 'dark' ? 'light' : 'dark';
    uiTheme = next;
    try {
      localStorage.setItem('aronium_ui_theme', next);
    } catch {
      // ignore storage errors
    }
    if (!loading) {
      handleSavePrefs({ theme: next });
    }
  }

  async function handleVerifyPinModal() {
    if (!/^\d{5}$/.test(pinPromptInput.trim())) {
      pinPromptError = 'Ingresa un PIN numérico de exactamente 5 dígitos.';
      return;
    }
    try {
      const res = await runAroniumCLI<{ valid: boolean }>([
        'config',
        'verify-pin',
        '--pin',
        pinPromptInput.trim(),
        '--json',
      ]);
      if (!res.valid) {
        pinPromptError = 'PIN incorrecto. Verifica los 5 dígitos.';
        return;
      }
      sessionPin = pinPromptInput.trim();
      advancedUnlocked = true;
      const action = pendingPinAction;
      pinModalVisible = false;
      pendingPinAction = null;
      pinPromptInput = '';
      pinPromptError = '';

      if (action === 'enter_advanced') {
        uiMode = 'advanced';
        try {
          localStorage.setItem('aronium_ui_mode', 'advanced');
        } catch {
          // ignore
        }
      } else if (action === 'delete_account' && confirmDeleteAccount) {
        await handleDeletePaidAccount(confirmDeleteAccount);
      } else if (action === 'reset_accounts_table') {
        showResetTableModal = true;
      }
    } catch (e: any) {
      pinPromptError = e?.message || 'Error validando PIN';
    }
  }

  async function handleConfigurePin() {
    if (!/^\d{5}$/.test(newPinInput.trim())) {
      showToast('El nuevo PIN debe tener exactamente 5 dígitos numéricos (ej. 12345).', true);
      return;
    }
    if (newPinInput.trim() !== confirmNewPinInput.trim()) {
      showToast('La confirmación del PIN de 5 dígitos no coincide.', true);
      return;
    }
    loading = true;
    try {
      const args = ['config', 'set-pin', '--new-pin', newPinInput.trim()];
      if (hasAdvancedPin) {
        args.push('--current-pin', curPinInput.trim() || sessionPin);
      }
      args.push('--json');
      await runAroniumCLI(args);
      hasAdvancedPin = true;
      sessionPin = newPinInput.trim();
      advancedUnlocked = true;
      curPinInput = '';
      newPinInput = '';
      confirmNewPinInput = '';
      await refreshAll();
      showToast('PIN numérico de 5 dígitos guardado. El Modo Avanzado y la eliminación quedan protegidos.');
    } catch (e: any) {
      showToast(`No se pudo guardar el PIN: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleClearAdvancedPin() {
    loading = true;
    try {
      await runAroniumCLI([
        'config',
        'clear-pin',
        '--current-pin',
        curPinInput.trim() || sessionPin,
        '--json',
      ]);
      hasAdvancedPin = false;
      sessionPin = '';
      curPinInput = '';
      await refreshAll();
      showToast('PIN de protección eliminado.');
    } catch (e: any) {
      showToast(`No se pudo quitar el PIN: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleRunRepair() {
    repairing = true;
    try {
      const res = await runAroniumCLI<RepairReport>(['repair', '--json']);
      lastRepairReport = res;
      await refreshAll();
      showToast(
        res.healthy
          ? `✓ Reparación y Health Check OK: ${res.pos_db_product_count} productos en pos.db, ${res.accounts_count} cuentas listas.`
          : 'Health Check completado con advertencias. Revisa el detalle técnico abajo.',
        !res.healthy
      );
    } catch (e: any) {
      showToast(`Error ejecutando reparación integral: ${e?.message || e}`, true);
    } finally {
      repairing = false;
    }
  }

  async function handleResetAccountsTable() {
    if (hasAdvancedPin && !advancedUnlocked) {
      pendingPinAction = 'reset_accounts_table';
      pinPromptInput = '';
      pinPromptError = '';
      pinModalVisible = true;
      return;
    }
    resettingAccountsTable = true;
    try {
      const args = ['accounts', 'reset'];
      if (hasAdvancedPin) {
        args.push('--pin', sessionPin || curPinInput.trim());
      }
      args.push('--json');
      const res = await runAroniumCLI<{ reset: boolean; backup_path: string }>(args);
      showResetTableModal = false;
      await refreshAll();
      showToast(
        `Tabla cuentas.db borrada y recreada limpia. Respaldo previo guardado en: ${res.backup_path || 'backups/'}`
      );
    } catch (e: any) {
      showToast(`Error reiniciando tabla de cuentas: ${e?.message || e}`, true);
    } finally {
      resettingAccountsTable = false;
    }
  }

  async function handleDeletePaidAccount(acc: CustomerAccount) {
    if (hasAdvancedPin && !advancedUnlocked) {
      confirmDeleteAccount = acc;
      pendingPinAction = 'delete_account';
      pinPromptInput = '';
      pinPromptError = '';
      pinModalVisible = true;
      return;
    }
    if (acc.saldo_actual !== 0) {
      showToast(
        `No se puede eliminar la cuenta "${acc.nombre}" porque aún tiene saldo pendiente ($${acc.saldo_actual.toLocaleString()}).`,
        true
      );
      confirmDeleteAccount = null;
      return;
    }
    deletingAccount = true;
    try {
      const args = [
        'accounts',
        'delete',
        '--account-id',
        accKeyOf(acc),
        '--name',
        acc.nombre,
        autoSyncLive ? '--sync=true' : '--sync=false',
      ];
      if (hasAdvancedPin && sessionPin) {
        args.push('--pin', sessionPin);
      }
      args.push('--json');
      await runAroniumCLI(args);
      confirmDeleteAccount = null;
      await refreshAll();
      showToast(`Cuenta pagada [${acc.id}] ${acc.nombre} eliminada definitivamente de cuentas.db.`);
    } catch (e: any) {
      showToast(`No se pudo eliminar la cuenta: ${e?.message || e}`, true);
    } finally {
      deletingAccount = false;
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

  $effect(() => {
    if (typeof document !== 'undefined') {
      document.body.classList.toggle('theme-light', uiTheme === 'light');
    }
  });

  onMount(() => {
    try {
      const savedMode = localStorage.getItem('aronium_ui_mode');
      if (savedMode === 'basic' || savedMode === 'advanced') {
        uiMode = savedMode;
      }
      const savedTheme = localStorage.getItem('aronium_ui_theme');
      if (savedTheme === 'light' || savedTheme === 'dark') {
        uiTheme = savedTheme;
      }
    } catch {
      // ignore
    }
    refreshAll();
  });
</script>

<div class="app-shell {uiCompactMode ? 'compact-43' : ''} {uiTheme === 'light' ? 'theme-light' : ''}" style="--ui-zoom: {uiScalePercent / 100};">
  <!-- 5-DIGIT PIN SECURITY MODAL -->
  {#if pinModalVisible}
    <div class="modal-backdrop">
      <div class="modal-card">
        <div class="card-title" style="margin-bottom: 10px;">
          <span>🔒 Autorización con PIN de 5 Dígitos</span>
          <button
            class="btn btn-outline"
            style="padding: 4px 10px;"
            onclick={() => {
              pinModalVisible = false;
              pendingPinAction = null;
              confirmDeleteAccount = null;
            }}
          >
            Cancelar
          </button>
        </div>
        <p style="font-size: 13px; color: var(--text-muted); margin-top: 0;">
          Ingresa el PIN numérico de 5 dígitos para acceder al Modo Avanzado o autorizar eliminaciones/reinicio.
        </p>
        <input
          class="input"
          type="password"
          inputmode="numeric"
          maxlength="5"
          placeholder="••••• (5 dígitos)"
          bind:value={pinPromptInput}
          onkeydown={(e) => {
            if (e.key === 'Enter') handleVerifyPinModal();
          }}
        />
        {#if pinPromptError}
          <div style="color: var(--accent-red); font-size: 12.5px; margin-top: 8px; font-weight: 700;">
            {pinPromptError}
          </div>
        {/if}
        <button class="btn btn-green" style="width: 100%; margin-top: 14px;" onclick={handleVerifyPinModal}>
          Desbloquear y Continuar
        </button>
      </div>
    </div>
  {/if}

  <!-- CONFIRM RESET / DROP & RECREATE ACCOUNTS TABLE MODAL -->
  {#if showResetTableModal}
    <div class="modal-backdrop">
      <div class="modal-card" style="border-color: var(--accent-red);">
        <div class="card-title" style="color: var(--accent-red);">
          <span>⚠️ Borrar y Volver a Crear Tabla de Cuentas</span>
        </div>
        <p style="font-size: 13px; color: var(--text-danger-soft); line-height: 1.5; margin-top: 0;">
          Esta acción guardará una copia de respaldo automática en <code>backups/</code>, eliminará por completo
          las tablas de <code>cuentas.db</code> y las volverá a crear vacías (esquema v3 en hora local).
        </p>
        <div style="display: flex; gap: 10px; flex-wrap: wrap;">
          <button
            class="btn btn-red"
            disabled={resettingAccountsTable}
            onclick={handleResetAccountsTable}
          >
            {resettingAccountsTable ? 'Recreando tabla limpia...' : 'Sí, Borrar y Recrear cuentas.db'}
          </button>
          <button
            class="btn btn-outline"
            disabled={resettingAccountsTable}
            onclick={() => (showResetTableModal = false)}
          >
            Cancelar
          </button>
        </div>
      </div>
    </div>
  {/if}

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
        {doctor?.pos_db_readable
          ? `✓ Productos Aronium OK (${doctor?.pos_db_product_count ?? snapshot?.inventory_summary?.total_products ?? 0})`
          : '⚠ Falta Base de Productos'}
      </span>
      <span class={doctor?.accounts_db_ready ? 'badge badge-ok' : 'badge badge-warn'}>
        {doctor?.accounts_db_ready
          ? `✓ Base Cuentas (${doctor?.accounts_count ?? accountsReport?.total_cuentas ?? 0})`
          : 'Base Cuentas Pendiente'}
      </span>
      {#if hasAdvancedPin && uiMode === 'advanced'}
        <button
          class="btn btn-outline"
          style="padding: 6px 10px; font-size: 12px;"
          onclick={() => {
            advancedUnlocked = false;
            sessionPin = '';
            setUIMode('basic');
          }}
        >
          🔒 Bloquear Avanzado
        </button>
      {/if}
      <button class="btn btn-outline" style="padding: 6px 12px;" onclick={() => (showDbModal = !showDbModal)}>
        Bases de Datos
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
          <div style="font-size: 13px; margin-top: 4px; color: var(--text-amber-soft);">
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
            <label class="field-label" style="margin-top: 10px;">
              Contraseña de Base de Datos Aronium (Opcional — dejar vacía si no usa cifrado SQLite)
              <input
                class="input"
                type="password"
                bind:value={customPosDbPassword}
                placeholder={configInfo?.pos_db_password_set ? '•••••••• (ya configurada)' : 'Opcional (en Aronium Lite estándar va vacío)'}
              />
            </label>
            {#if configInfo?.pos_db_password_set}
              <label style="display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-muted); margin-top: 4px;">
                <input type="checkbox" bind:checked={clearPosDbPassword} /> Quitar contraseña configurada
              </label>
            {/if}
          </div>
          <div>
            <label class="field-label">
              Base Dedicada de Cuentas de Clientes (cuentas.db)
              <div style="display: flex; gap: 8px; margin-top: 4px;">
                <input class="input" bind:value={customAccountsDbPath} placeholder="C:\Users\...\AppData\Local\AroniumUtility\cuentas.db" />
                <button class="btn btn-outline" onclick={() => handlePickDatabase('accounts')}>Buscar...</button>
              </div>
            </label>
            <div style="margin-top: 10px; padding: 10px 12px; border-radius: 8px; background: var(--bg-primary); border: 1px solid var(--border); font-size: 12.5px;">
              <strong>Productos leídos de Aronium:</strong> {doctor?.pos_db_product_count ?? 0} producto(s)<br />
              <strong>Cuentas activas/registradas:</strong> {doctor?.accounts_count ?? accountsReport?.total_cuentas ?? 0}
            </div>
          </div>
        </div>
        <div style="display: flex; gap: 10px; flex-wrap: wrap; align-items: center;">
          <button class="btn btn-green" disabled={loading} onclick={() => handleSaveDatabasePaths(false, false)}>
            Guardar y Conectar Bases de Datos
          </button>
          <button class="btn" disabled={backingUpAllDBs} onclick={handleBackupAllDBsToDrive}>
            {backingUpAllDBs ? 'Respaldando todas las .db...' : '☁️ Respaldar Todas las Bases (.db) a Drive'}
          </button>
          <button class="btn btn-outline" disabled={repairing} onclick={handleRunRepair}>
            {repairing ? 'Reparando...' : '🛠️ Reparar y Verificar Conexión'}
          </button>
          <button class="btn btn-outline" onclick={() => handleSaveDatabasePaths(true, false)}>
            Ruta por Defecto
          </button>
          <button class="btn btn-outline" onclick={() => handleSaveDatabasePaths(false, true)}>
            Crear BD Prueba
          </button>
          <button
            class="btn btn-red"
            disabled={resettingAccountsTable}
            onclick={() => {
              if (hasAdvancedPin && !advancedUnlocked) {
                pendingPinAction = 'reset_accounts_table';
                pinPromptInput = '';
                pinPromptError = '';
                pinModalVisible = true;
              } else {
                showResetTableModal = true;
              }
            }}
          >
            🗑️ Borrar Tabla Cuentas y Volver a Crear
          </button>
        </div>
        {#if lastAllDBsBackup}
          <div style="margin-top: 10px; padding: 10px 12px; border-radius: 8px; background: rgba(16, 185, 129, 0.12); border: 1px solid var(--accent-green); font-size: 12px; color: var(--text-green-soft);">
            <strong>✓ {lastAllDBsBackup.message}</strong><br />
            Paquete ZIP: <code>{lastAllDBsBackup.zip_archive_path}</code>
            {#if lastAllDBsBackup.documents_accounts_copy}
              <br />Copia Mis Documentos: <code>{lastAllDBsBackup.documents_accounts_copy}</code>
            {/if}
          </div>
        {/if}
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
                  <span>ID de la Cuenta / Ubicación (Texto - Número) *</span>
                  <div class="compound-id-box">
                    <input
                      class="compound-id-part"
                      placeholder="Texto (Ej. ABC)"
                      value={formIdPrefix}
                      oninput={(e) => handleIdPrefixInput((e.currentTarget as HTMLInputElement).value)}
                    />
                    <span class="compound-id-sep">-</span>
                    <input
                      bind:this={formIdNumberEl}
                      class="compound-id-part"
                      inputmode="numeric"
                      placeholder="Número (Ej. 123)"
                      value={formIdNumber}
                      oninput={(e) => handleIdNumberInput((e.currentTarget as HTMLInputElement).value)}
                    />
                  </div>
                  <div style="display: flex; align-items: center; gap: 10px; flex-wrap: wrap;">
                    <span class="id-preview-pill">
                      Vista previa ID: {combinedFormId || 'ABC-123'}
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
                  <input class="input" placeholder="Ej. Nombre Apellido" bind:value={formName} />
                </label>
                <label class="field-label">
                  Teléfono (Opcional)
                  <input class="input" placeholder="Opcional · Ej. 000-000-0000" bind:value={formPhone} />
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
                    placeholder="Opcional · Ej. Torre A, Apartamento 110"
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
                  placeholder="Opcional · Ej. Nota o referencia breve"
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
                  <label style="display: flex; align-items: center; gap: 8px; margin-top: 6px; color: var(--text-danger-soft);">
                    <input type="checkbox" bind:checked={allowOverLimitConfirm} />
                    Autorizo guardar superando el tope de crédito
                  </label>
                </div>
              {/if}
            </div>

            <label
              style="display: flex; align-items: center; gap: 8px; margin-top: 12px; font-size: 12.5px; color: var(--text-muted); cursor: pointer;"
            >
              <input
                type="checkbox"
                checked={autoSyncLive}
                onchange={(e) => handleSavePrefs({ autoSync: (e.currentTarget as HTMLInputElement).checked })}
              />
              <span>
                Sincronizar en vivo con Google Drive al guardar (Desmarcado = Guardar solo en local al instante)
              </span>
            </label>

            <button
              class="btn btn-green"
              style="width: 100%; margin-top: 12px; padding: 13px; font-size: 14.5px;"
              disabled={savingAccount}
              onclick={handleSaveAccountAndItems}
            >
              {savingAccount
                ? autoSyncLive
                  ? 'Guardando y Sincronizando...'
                  : 'Guardando en Local...'
                : accountActionMode === 'new'
                  ? autoSyncLive
                    ? 'Guardar Cuenta y Sincronizar con Drive'
                    : 'Guardar Cuenta y Artículos (Solo Local)'
                  : autoSyncLive
                    ? 'Anotar Artículos y Sincronizar con Drive'
                    : 'Anotar Artículos en Cuenta (Solo Local)'}
            </button>
          </div>

          <!-- Right Card: Quick Punctual Value + Barcode Scanner + Live Aronium Product Search + Cart -->
          <div class="card">
            <!-- Quick Punctual Value Annotator ABOVE Barcode Scanner -->
            <div class="quick-value-box">
              <div style="font-size: 13.5px; font-weight: 800; color: var(--text-amber-soft); margin-bottom: 8px;">
                Agregar Valor Puntual
              </div>
              <div style="display: grid; grid-template-columns: 1fr; gap: 8px;">
                <label class="field-label" style="margin-bottom: 0;">
                  Descripción opcional (referente del artículo)
                  <input
                    class="input"
                    placeholder="Opcional · Ej. Producto o descripción breve"
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
              <div style="font-size: 12.5px; color: var(--text-green-soft); margin-bottom: 8px; font-weight: 600;">
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
                <div style="font-size: 13px; color: var(--text-amber-soft); font-weight: 700; margin-bottom: 8px;">
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
              disabled={recordingPayment}
              onclick={handleRecordPayment}
            >
              {recordingPayment
                ? 'Registrando Pago...'
                : autoSyncLive
                  ? `Confirmar Pago para ${activePayAccount.nombre} y Sincronizar Drive`
                  : `Confirmar Pago en Local para ${activePayAccount.nombre}`}
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
              {#if driveStatus?.connected}
                ✓ Cuenta vinculada: <strong>{driveStatus.account_email || 'Google Drive activo'}</strong>. Tus cuentas e inventario se respaldan automáticamente en <code>{driveStatus?.master_workbook_name}</code>.
              {:else if driveStatus?.client_id_configured}
                Haz clic en <strong>Conectar con Google (1 Clic)</strong> para iniciar sesión con tu cuenta de Google y guardar automáticamente tus cuentas e inventario en Drive.
              {:else}
                Haz clic en <strong>Conectar con Google (1 Clic)</strong> para vincular tu cuenta de Google en un solo paso (sin configuraciones complejas).
              {/if}
            </p>
            <label
              style="display: flex; align-items: center; gap: 10px; margin-bottom: 14px; padding: 10px 12px; border-radius: 8px; background: var(--bg-primary); border: 1px solid var(--border); font-size: 13px; cursor: pointer;"
            >
              <input
                type="checkbox"
                checked={autoSyncLive}
                onchange={(e) => handleSavePrefs({ autoSync: (e.currentTarget as HTMLInputElement).checked })}
              />
              <span>
                <strong>Sincronizar automáticamente en vivo</strong> después de cada anotación o pago
                <span style="display: block; font-size: 11.5px; color: var(--text-muted);">
                  Recomendado desmarcado para atención rápida sin esperas; luego sincronizas al cierre de caja con el botón verde.
                </span>
              </span>
            </label>
            <div style="display: flex; gap: 10px; flex-wrap: wrap;">
              <button class="btn" onclick={() => handleDriveWebLogin(true)}>
                {driveStatus?.connected ? 'Cambiar Cuenta de Google (1 Clic)' : 'Conectar con Google (1 Clic)'}
              </button>
              <button class="btn btn-green" disabled={syncingDrive} onclick={handleSyncDrive}>
                {syncingDrive ? 'Sincronizando Drive...' : 'Sincronizar con Drive Ahora'}
              </button>
              <button class="btn btn-amber" disabled={backingUpAllDBs} onclick={handleBackupAllDBsToDrive}>
                {backingUpAllDBs ? 'Subiendo BDs a Drive...' : '☁️ Respaldar Todas las .db en Drive Ahora'}
              </button>
            </div>
            {#if lastSyncResult}
              <div style="margin-top: 14px; font-size: 12.5px; color: var(--accent-green);">
                ✓ Archivo actualizado: <code>{lastSyncResult.master_workbook_path}</code>
              </div>
            {/if}
            {#if lastAllDBsBackup}
              <div style="margin-top: 10px; font-size: 12.5px; color: var(--accent-blue);">
                ✓ {lastAllDBsBackup.message}
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

          <button
            class="nav-btn {advTab === 'logs' ? 'active' : ''}"
            onclick={() => {
              advTab = 'logs';
              handleLoadDebugLogs();
            }}
          >
            <span>7. Logs de Debug</span>
            <span>🐞</span>
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
            disabled={syncingDrive}
            onclick={handleSyncDrive}
          >
            {syncingDrive ? 'Sincronizando...' : 'Sincronizar Drive Ahora'}
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
            <button class="btn btn-outline" disabled={refreshing} onclick={refreshAll}>
              {refreshing ? 'Actualizando...' : 'Actualizar Datos'}
            </button>
            <button class="btn" disabled={syncingDrive} onclick={handleSyncDrive}>
              {syncingDrive ? 'Sincronizando...' : 'Sincronizar Drive + Backup'}
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
          {#if confirmDeleteAccount}
            <div
              class="card"
              style="border-color: var(--accent-red); background: rgba(248, 113, 113, 0.12); margin-bottom: 14px;"
            >
              <div class="card-title" style="color: var(--text-danger-soft);">
                <span>Confirmar Eliminación de Cuenta Pagada</span>
                <button class="btn btn-outline" onclick={() => (confirmDeleteAccount = null)}>Cancelar</button>
              </div>
              <p style="font-size: 13.5px; color: var(--text-danger-soft); margin-top: 0;">
                ¿Eliminar definitivamente la cuenta <strong>[{confirmDeleteAccount.id}] {confirmDeleteAccount.nombre}</strong>?
                Su saldo actual es <strong>$0</strong> (totalmente pagada). Esta acción borrará su historial de
                movimientos en <code>cuentas.db</code> y actualizará Google Drive.
              </p>
              <div style="display: flex; gap: 10px;">
                <button
                  class="btn btn-red"
                  disabled={deletingAccount}
                  onclick={() => confirmDeleteAccount && handleDeletePaidAccount(confirmDeleteAccount)}
                >
                  {deletingAccount ? 'Eliminando...' : 'Sí, Eliminar Cuenta Pagada'}
                </button>
                <button class="btn btn-outline" disabled={deletingAccount} onclick={() => (confirmDeleteAccount = null)}>
                  No, Conservar
                </button>
              </div>
            </div>
          {/if}

          <div class="card">
            <div class="card-title">
              <span>Libro Mayor de Cuentas (<code>{accountsReport?.accounts_db_path}</code>)</span>
              <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                <button class="btn btn-green" onclick={() => { setUIMode('basic'); basicTab = 'create'; }}>
                  Abrir Creador + Escáner
                </button>
                <button class="btn btn-red" onclick={() => (showResetTableModal = true)}>
                  🗑️ Borrar y Recrear Tabla Cuentas
                </button>
              </div>
            </div>
            <div class="table-scroll">
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
                  <th>Acciones</th>
                </tr>
              </thead>
              <tbody>
                {#each accountsReport?.cuentas || [] as acc}
                  <tr>
                    <td><code>{acc.id}</code></td>
                    <td><strong>{acc.nombre}</strong></td>
                    <td>{acc.telefono || '—'}</td>
                    <td>${acc.total_cargos.toLocaleString()}</td>
                    <td>${acc.total_abonos.toLocaleString()}</td>
                    <td><strong>${acc.saldo_actual.toLocaleString()}</strong></td>
                    <td>${acc.tope_credito.toLocaleString()}</td>
                    <td><span class={statusBadgeClass(acc.estado)}>{acc.estado}</span></td>
                    <td>
                      {#if acc.saldo_actual === 0}
                        <button
                          class="btn btn-red"
                          style="padding: 5px 10px; font-size: 12px;"
                          disabled={deletingAccount}
                          onclick={() => (confirmDeleteAccount = acc)}
                        >
                          Eliminar
                        </button>
                      {:else}
                        <span class="badge badge-warn" title="Solo se puede eliminar cuando el saldo pendiente sea $0">
                          Saldo pendiente
                        </span>
                      {/if}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
            </div>
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
              <label
                style="display: flex; align-items: center; gap: 8px; font-size: 12.5px; color: var(--text-muted); margin-bottom: 12px; cursor: pointer;"
              >
                <input
                  type="checkbox"
                  checked={autoSyncLive}
                  onchange={(e) => handleSavePrefs({ autoSync: (e.currentTarget as HTMLInputElement).checked })}
                />
                <span>Sincronizar automáticamente en vivo tras cada movimiento (Opcional)</span>
              </label>
              <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                <button class="btn" onclick={() => handleDriveWebLogin(true)}>Conectar en 1 Clic</button>
                <button class="btn btn-outline" onclick={() => handleDriveWebLogin(false)}>Configurar Credenciales / Portal</button>
                <button class="btn btn-green" disabled={syncingDrive} onclick={handleSyncDrive}>
                  {syncingDrive ? 'Sincronizando...' : 'Sincronizar Ahora'}
                </button>
              </div>
            </div>
            <div class="card">
              <h3 class="card-title">Respaldo Explícito de Todas las Bases (.db) a Drive y PC</h3>
              <p style="font-size: 12.5px; color: var(--text-muted); margin-top: 0;">
                Toma copia consistente en caliente de <code>pos.db</code> (Aronium), <code>cuentas.db</code>, <code>aronium_utility_state.db</code> y <code>aronium-util.yaml</code>, guarda un <code>.zip</code> en PC + copia en Mis Documentos y lo sube directo a la carpeta <code>/Backups_BD</code> de Google Drive.
              </p>
              <button class="btn btn-amber" style="width: 100%; margin-bottom: 12px;" disabled={backingUpAllDBs} onclick={handleBackupAllDBsToDrive}>
                {backingUpAllDBs ? 'Respaldando y Subiendo .db a Drive...' : '☁️ Respaldar Todas las Bases (.db) en Drive Ahora'}
              </button>
              {#if lastAllDBsBackup}
                <div style="font-size: 12px; padding: 10px; border-radius: 8px; background: var(--bg-primary); border: 1px solid var(--border); margin-bottom: 12px;">
                  <strong>{lastAllDBsBackup.message}</strong><br />
                  Drive conectado: <strong>{lastAllDBsBackup.drive_connected ? 'Sí (subido a la nube)' : 'No (respaldado localmente)'}</strong><br />
                  ZIP: <code>{lastAllDBsBackup.zip_archive_path}</code>
                  {#if lastAllDBsBackup.files?.length}
                    <div style="margin-top: 6px;">
                      Archivos: {lastAllDBsBackup.files.map((f) => `${f.name} (${Math.round(f.size_bytes / 1024)} KB)`).join(', ')}
                    </div>
                  {/if}
                </div>
              {/if}

              <h4 style="margin: 12px 0 8px 0; font-size: 13px; color: var(--accent-blue);">Respaldo Cifrado Individual AES-256-GCM (pos.db)</h4>
              <input class="input" type="password" placeholder="Frase AES-256-GCM opcional" bind:value={backupPassphrase} />
              <button class="btn btn-outline" style="margin-top: 10px; width: 100%;" onclick={handleCreateBackup}>
                Crear Respaldo AES-256 en Caliente (pos.db)
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
          <!-- System Repair & Integral Health Check (BD, Products, Drive, CLI) -->
          <div class="card" style="border-color: var(--accent-green); margin-bottom: 16px;">
            <div class="card-title">
              <span>🛠️ Reparación Integral y Health Check (BD, Inventario, Drive, CLI)</span>
              <button class="btn btn-green" disabled={repairing || loading} onclick={handleRunRepair}>
                {repairing ? 'Reparando y Verificando...' : 'Reparar y Verificar Todo Ahora'}
              </button>
            </div>
            <p style="font-size: 13px; color: var(--text-muted); margin-top: 0;">
              Valida y repara en un clic las variables de entorno / PATH, detecta y prueba la carga de productos desde <code>pos.db</code>, verifica la migración y respaldos de <code>cuentas.db</code> y revisa Google Drive.
            </p>
            {#if lastRepairReport}
              <div class="table-scroll">
                <table>
                  <thead>
                    <tr><th>Componente</th><th>Estado</th><th>Resultado</th></tr>
                  </thead>
                  <tbody>
                    {#each lastRepairReport.steps as step}
                      <tr>
                        <td><strong>{step.component}</strong></td>
                        <td><span class={step.ok ? 'badge badge-ok' : 'badge badge-danger'}>{step.ok ? 'OK' : 'ATENCIÓN'}</span></td>
                        <td>{step.message}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          </div>

          <!-- Separated Card 1: Timezone | Separated Card 2: Visual Scale & Dark/Light Theme Switch -->
          <div class="panel-grid">
            <div class="card">
              <div class="card-title">
                <span>🕒 Zona Horaria de Registro</span>
                <span class="badge badge-ok">{effectiveTimezone || 'Local'}</span>
              </div>
              <p style="font-size: 12.5px; color: var(--text-muted); margin-top: 0; margin-bottom: 12px;">
                Define si los cargos, abonos y comprobantes usan la hora automática de Windows o una zona horaria manual para cuadrar con tus cámaras de seguridad.
              </p>
              <label class="field-label">
                Zona Horaria de Registro (Sistema o Manual)
                <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                  <select class="select" style="flex: 1; min-width: 180px;" bind:value={timezoneInput}>
                    <option value="Local">Hora del Sistema Windows (Automática)</option>
                    <option value="America/Bogota">Colombia — America/Bogota (UTC-5)</option>
                    <option value="UTC-5">UTC-5 Fijo</option>
                    <option value="America/Lima">Perú — America/Lima (UTC-5)</option>
                    <option value="America/Mexico_City">México — America/Mexico_City</option>
                    <option value="America/Santiago">Chile — America/Santiago</option>
                    <option value="America/Buenos_Aires">Argentina — America/Buenos_Aires (UTC-3)</option>
                    <option value="custom">Otra zona horaria manual...</option>
                  </select>
                  {#if timezoneInput === 'custom'}
                    <input class="input" style="max-width: 160px;" bind:value={customTimezoneValue} placeholder="Ej. America/Bogota o -05:00" />
                  {/if}
                </div>
              </label>
              <div style="font-size: 12px; color: var(--accent-blue); margin-top: 10px; margin-bottom: 14px;">
                Zona activa: <strong>{effectiveTimezone || 'Local'}</strong> · Hora efectiva: <code>{currentLocalTime || 'En sincronía con Windows'}</code>
              </div>
              <button class="btn" style="width: 100%;" disabled={loading} onclick={() => handleSavePrefs()}>
                Guardar Zona Horaria
              </button>
            </div>

            <div class="card">
              <div class="card-title">
                <span>🖥️ Escala Visual y Tema (Monitor 4:3 / 720p)</span>
                <span class="badge badge-ok">{uiScalePercent}% · {uiTheme === 'light' ? 'Modo Claro' : 'Modo Oscuro'}</span>
              </div>

              <div class="theme-switch-row">
                <div>
                  <div style="font-size: 13px; font-weight: 700; color: var(--text-main);">
                    Apariencia de Interfaz
                  </div>
                  <div style="font-size: 11.5px; color: var(--text-muted);">
                    Por defecto en Modo Oscuro · Cambia a Modo Claro si trabajas con mucha luz
                  </div>
                </div>
                <button
                  type="button"
                  role="switch"
                  aria-checked={uiTheme === 'light'}
                  aria-label="Cambiar entre Modo Oscuro y Modo Claro"
                  class="theme-switch-btn {uiTheme === 'light' ? 'is-light' : ''}"
                  onclick={handleToggleTheme}
                >
                  <span class="theme-switch-track">
                    <span class="theme-switch-thumb"></span>
                  </span>
                  <span>{uiTheme === 'light' ? '☀️ Modo Claro' : '🌙 Modo Oscuro'}</span>
                </button>
              </div>

              <label class="field-label">
                Tamaño de Interfaz Visual: <strong>{uiScalePercent}%</strong> (Ideal para monitor 4:3 / 720p: 85%–90%)
                <input type="range" min="75" max="125" step="5" bind:value={uiScalePercent} />
              </label>
              <div style="display: flex; gap: 6px; flex-wrap: wrap; margin: 8px 0;">
                {#each [80, 90, 100, 110] as preset}
                  <button
                    type="button"
                    class="btn {uiScalePercent === preset ? 'btn-green' : 'btn-outline'}"
                    style="padding: 4px 10px; font-size: 12px;"
                    onclick={() => (uiScalePercent = preset)}
                  >
                    {preset}%
                  </button>
                {/each}
              </div>
              <label style="display: flex; align-items: center; gap: 8px; font-size: 12.5px; color: var(--text-muted); margin-bottom: 12px; cursor: pointer;">
                <input type="checkbox" bind:checked={uiCompactMode} />
                <span>Modo Compacto 4:3 (Espaciado optimizado para 1024x768 / 720p)</span>
              </label>
              <button class="btn" style="width: 100%;" disabled={loading} onclick={() => handleSavePrefs()}>
                Guardar Escala Visual y Tema
              </button>
            </div>
          </div>

          <!-- Separate Card 3: 5-Digit PIN Security -->
          <div class="card">
            <div class="card-title">
              <span>🔐 PIN Numérico de 5 Dígitos (Modo Avanzado)</span>
              <span class={hasAdvancedPin ? 'badge badge-ok' : 'badge badge-warn'}>
                {hasAdvancedPin ? 'PIN Activo' : 'Sin PIN'}
              </span>
            </div>
            <p style="font-size: 12.5px; color: var(--text-muted); margin-top: 0;">
              Protege la entrada a <strong>Modo Avanzado</strong>, la eliminación de cuentas y el reinicio de <code>cuentas.db</code> con una clave numérica de 5 dígitos.
            </p>
            {#if hasAdvancedPin}
              <label class="field-label" style="margin-bottom: 8px;">
                PIN Actual (5 dígitos)
                <input class="input" type="password" inputmode="numeric" maxlength="5" bind:value={curPinInput} placeholder="•••••" />
              </label>
            {/if}
            <div class="form-grid" style="margin-bottom: 10px;">
              <label class="field-label">
                Nuevo PIN (5 números)
                <input class="input" type="password" inputmode="numeric" maxlength="5" bind:value={newPinInput} placeholder="Ej. 12345" />
              </label>
              <label class="field-label">
                Confirmar Nuevo PIN
                <input class="input" type="password" inputmode="numeric" maxlength="5" bind:value={confirmNewPinInput} placeholder="Ej. 12345" />
              </label>
            </div>
            <div style="display: flex; gap: 8px; flex-wrap: wrap;">
              <button class="btn btn-green" disabled={loading} onclick={handleConfigurePin}>
                {hasAdvancedPin ? 'Cambiar PIN de 5 Dígitos' : 'Activar PIN de 5 Dígitos'}
              </button>
              {#if hasAdvancedPin}
                <button class="btn btn-red" disabled={loading} onclick={handleClearAdvancedPin}>
                  Quitar PIN
                </button>
              {/if}
            </div>
          </div>

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
                  <div style="margin-top: 6px; font-weight: 700; color: var(--text-blue-soft);">
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
                style="padding: 10px 14px; border-radius: 8px; background: rgba(16, 185, 129, 0.14); border: 1px solid var(--accent-green); color: var(--text-green-soft); font-size: 13px; margin-bottom: 12px;"
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
              <div class="table-scroll">
              <table>
                <tbody>
                  <tr><td>pos.db (Solo Lectura)</td><td><code>{doctor?.pos_db_path}</code> ({doctor?.pos_db_readable ? `OK · ${doctor?.pos_db_product_count ?? 0} productos` : 'No encontrado / Error'})</td></tr>
                  <tr><td>cuentas.db (WAL)</td><td><code>{doctor?.accounts_db_path}</code> ({doctor?.accounts_db_ready ? `Lista · ${doctor?.accounts_count ?? 0} cuentas` : 'Error'})</td></tr>
                  <tr><td>PRAGMA quick_check</td><td>{doctor?.quick_check_result || 'N/A'}</td></tr>
                  <tr><td>Esquema Aronium</td><td>{doctor?.aronium_schema_version || 'N/A'}</td></tr>
                  <tr><td>Outbox Offline</td><td>{doctor?.pending_outbox_count || 0} pendientes</td></tr>
                </tbody>
              </table>
              </div>
              <div style="display: flex; gap: 8px; flex-wrap: wrap; margin-top: 12px;">
                <button class="btn btn-outline" onclick={() => (showDbModal = true)}>Cambiar Rutas / Contraseña BD</button>
                <button class="btn btn-red" onclick={() => (showResetTableModal = true)}>🗑️ Borrar y Recrear Tabla Cuentas</button>
              </div>
            </div>
            <div class="card">
              <h3 class="card-title">Inicio Automático en Windows & Bandeja del Sistema</h3>
              <label
                style="display: flex; align-items: center; gap: 10px; font-size: 13.5px; cursor: pointer; margin-bottom: 12px;"
              >
                <input
                  type="checkbox"
                  checked={Boolean(serviceStatus?.autostart?.enabled)}
                  onchange={(e) => handleToggleAutostart((e.currentTarget as HTMLInputElement).checked)}
                />
                <span>
                  <strong>Abrir automáticamente al encender el PC</strong> (Bandeja + UI + Monitor en vivo)
                </span>
              </label>
              <p style="font-size: 12.5px; color: var(--text-muted); margin-top: 0;">
                Estado: <code>{serviceStatus?.status}</code><br />
                También puedes marcar/desmarcar esta opción, actualizar o cerrar la app por completo haciendo clic
                derecho sobre el ícono de <strong>Aronium Utility</strong> en la bandeja de tareas de Windows.
              </p>
              <button class="btn btn-green" style="width: 100%;" onclick={handleInstallAutoStartService}>
                {serviceStatus?.autostart?.enabled ? 'Reparar / Re-registrar Acceso Directo Startup' : 'Activar Inicio Automático'}
              </button>
            </div>
          </div>
        {/if}

        {#if advTab === 'logs'}
          <div class="card" style="border-color: var(--accent-blue); margin-bottom: 16px;">
            <div class="card-title">
              <span>🐞 Diagnóstico de Ejecución & Logs de Debug (UI ↔ CLI Sidecar)</span>
              <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                <button class="btn btn-green" disabled={loadingLogs} onclick={handlePingCLI}>
                  🧪 Probar Comando CLI (Doctor Ping)
                </button>
                <button class="btn" disabled={loadingLogs} onclick={handleLoadDebugLogs}>
                  {loadingLogs ? 'Cargando...' : '🔄 Actualizar Logs'}
                </button>
                <button class="btn btn-outline" disabled={!debugLogs} onclick={handleCopyLogs}>
                  📋 Copiar Logs
                </button>
                <button class="btn btn-red" disabled={loadingLogs} onclick={handleClearLogs}>
                  🗑️ Limpiar Logs
                </button>
              </div>
            </div>

            {#if cliPingResult}
              <div
                style="padding: 10px 12px; border-radius: 8px; margin-bottom: 12px; border: 1px solid var(--border); background: var(--bg-primary); font-size: 12.5px; font-weight: 700;"
              >
                {cliPingResult}
              </div>
            {/if}

            {#if debugLogs}
              <div class="panel-grid" style="margin-bottom: 14px;">
                <div style="padding: 12px; border-radius: 10px; border: 1px solid var(--border); background: var(--bg-primary); font-size: 12.5px;">
                  <div><strong>Versión UI:</strong> <code>{debugLogs.ui_version || debugLogs.cli_version || '2.0.2'}</code></div>
                  <div><strong>Binario CLI Sidecar:</strong> <code>{debugLogs.cli_bin_path}</code></div>
                  <div>
                    <strong>CLI presente en disco:</strong>
                    <span class={debugLogs.cli_bin_exists ? 'badge badge-ok' : 'badge badge-danger'}>
                      {debugLogs.cli_bin_exists ? '✓ SÍ EXISTE' : '✗ NO ENCONTRADO / BLOQUEADO'}
                    </span>
                  </div>
                </div>
                <div style="padding: 12px; border-radius: 10px; border: 1px solid var(--border); background: var(--bg-primary); font-size: 12.5px;">
                  <div><strong>Carpeta AppData:</strong> <code>{debugLogs.app_data_dir}</code></div>
                  <div><strong>Archivo Debug Log:</strong> <code>{debugLogs.debug_log_path}</code></div>
                  <div><strong>Archivo Service Log:</strong> <code>{debugLogs.service_log_path}</code></div>
                </div>
              </div>

              <h4 style="margin: 0 0 8px 0; color: var(--accent-blue); font-size: 13.5px;">Registro de Invocaciones UI ↔ CLI (<code>debug.log</code>)</h4>
              <pre class="stmt-box" style="max-height: 290px; margin-bottom: 16px;">{debugLogs.debug_log || '(Sin registros)'}</pre>

              <h4 style="margin: 0 0 8px 0; color: var(--accent-blue); font-size: 13.5px;">Registro del Servicio / Watcher (<code>service.log</code>)</h4>
              <pre class="stmt-box" style="max-height: 220px;">{debugLogs.service_log || '(Sin registros)'}</pre>
            {:else}
              <p style="font-size: 13px; color: var(--text-muted);">
                Haz clic en <strong>Actualizar Logs</strong> o <strong>Probar Comando CLI</strong> para inspeccionar el historial de ejecución en tiempo real.
              </p>
            {/if}
          </div>
        {/if}
      </main>
    </div>
  {/if}
</div>
