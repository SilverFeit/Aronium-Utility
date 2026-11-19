<script lang="ts">
  import { onMount } from 'svelte';
  import { runAroniumCLI } from './lib/ipc';
  import type {
    BackupManifest,
    CustomerCreditAccount,
    DailyClosingReport,
    DashboardSnapshot,
    DoctorReport,
    DriveStatus,
    InventoryItem,
    KardexEntry,
    SyncResult,
  } from './lib/types';

  type TabId = 'dashboard' | 'credit' | 'inventory' | 'registers' | 'drive' | 'doctor';

  let activeTab = $state<TabId>('dashboard');
  let loading = $state(false);
  let toastMessage = $state('');
  let toastError = $state(false);

  // CLI JSON state models (100% populated from `aronium-util <cmd> --json`)
  let snapshot = $state<DashboardSnapshot | null>(null);
  let dailyReport = $state<DailyClosingReport | null>(null);
  let doctor = $state<DoctorReport | null>(null);
  let driveStatus = $state<DriveStatus | null>(null);
  let kardexList = $state<KardexEntry[]>([]);
  let lastSyncResult = $state<SyncResult | null>(null);
  let lastBackup = $state<BackupManifest | null>(null);
  let serviceStatus = $state<{ status: string; log_path: string } | null>(null);

  // Credit Account Form & Filters
  let customerSearch = $state('');
  let noteCustomerTarget = $state('');
  let noteText = $state('');
  let notePromiseDate = $state('');
  let limitCustomerID = $state('');
  let limitAmount = $state('');
  let selectedStatement = $state('');
  let selectedStatementCustomer = $state('');

  // Inventory Filters
  let invSearch = $state('');
  let invStatusFilter = $state<'ALL' | 'OK' | 'STOCK_BAJO' | 'AGOTADO' | 'STOCK_NEGATIVO'>('ALL');
  let invSubView = $state<'catalog' | 'kardex'>('catalog');

  // Backup Passphrase
  let backupPassphrase = $state('');

  function showToast(msg: string, isErr = false) {
    toastMessage = msg;
    toastError = isErr;
  }

  async function refreshAll() {
    loading = true;
    try {
      const [snap, daily, doc, drv, srv] = await Promise.all([
        runAroniumCLI<DashboardSnapshot>(['admin', '--json']),
        runAroniumCLI<DailyClosingReport>(['reports', 'daily', '--deliver=false', '--json']),
        runAroniumCLI<DoctorReport>(['doctor', '--json']),
        runAroniumCLI<DriveStatus>(['drive', 'status', '--json']),
        runAroniumCLI<{ status: string; log_path: string }>(['service', 'status', '--json']),
      ]);
      snapshot = snap;
      dailyReport = daily;
      doctor = doc;
      driveStatus = drv;
      serviceStatus = srv;
    } catch (e: any) {
      showToast(`Error consultando CLI aronium-util: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function loadKardex() {
    try {
      kardexList = await runAroniumCLI<KardexEntry[]>(['inventory', 'kardex', '--json']);
    } catch (e: any) {
      showToast(`Error cargando Kardex: ${e?.message || e}`, true);
    }
  }

  async function handleAddCreditNote() {
    if (!noteCustomerTarget.trim() || !noteText.trim()) {
      showToast('Ingresa el nombre o ID del cliente y la anotación de la cuenta.', true);
      return;
    }
    loading = true;
    try {
      const args = ['credit', 'note', 'add'];
      if (notePromiseDate.trim()) {
        args.push('--promise', notePromiseDate.trim());
      }
      args.push('--json', noteCustomerTarget.trim(), noteText.trim());
      await runAroniumCLI(args);

      // Immediately sync updated customer account to Google Drive table via CLI
      lastSyncResult = await runAroniumCLI<SyncResult>(['sync', '--json']);
      noteText = '';
      await refreshAll();
      showToast(`Anotación guardada para "${noteCustomerTarget}" y sincronizada en Google Drive.`);
    } catch (e: any) {
      showToast(`Error guardando anotación: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleSetCreditLimit() {
    if (!limitCustomerID.trim() || !limitAmount.trim()) {
      showToast('Ingresa el ID del cliente y el nuevo límite de crédito.', true);
      return;
    }
    loading = true;
    try {
      await runAroniumCLI(['credit', 'limit', '--json', limitCustomerID.trim(), limitAmount.trim()]);
      await refreshAll();
      showToast(`Límite de crédito actualizado para el cliente #${limitCustomerID}.`);
    } catch (e: any) {
      showToast(`Error actualizando límite: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleViewStatement(customerName: string) {
    loading = true;
    try {
      const res = await runAroniumCLI<{ statement: string }>([
        'credit',
        'statement',
        '--json',
        customerName,
      ]);
      selectedStatementCustomer = customerName;
      selectedStatement = res.statement;
    } catch (e: any) {
      showToast(`Error generando estado de cuenta: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleSyncDrive() {
    loading = true;
    try {
      lastSyncResult = await runAroniumCLI<SyncResult>(['sync', '--json']);
      await refreshAll();
      showToast(
        `Sincronización completada: ${lastSyncResult.master_workbook_path} (${lastSyncResult.customer_files?.length || 0} cuentas de clientes).`
      );
    } catch (e: any) {
      showToast(`Error sincronizando con Google Drive: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  async function handleDriveWebLogin() {
    showToast('Abriendo navegador para autenticación OAuth 2.0 con Google Drive...');
    try {
      await runAroniumCLI(['drive', 'login', '--timeout', '120', '--json']);
      await refreshAll();
      showToast('¡Google Drive vinculado exitosamente!');
    } catch (e: any) {
      showToast(`Autenticación web en espera o finalizada: ${e?.message || e}`, true);
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
      showToast(`Respaldo en caliente creado: ${lastBackup.archive_path}`);
    } catch (e: any) {
      showToast(`Error creando respaldo: ${e?.message || e}`, true);
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
      showToast(`Error registrando servicio: ${e?.message || e}`, true);
    } finally {
      loading = false;
    }
  }

  function statusBadgeClass(status: string): string {
    if (status === 'OK' || status === 'AL_DIA' || status === 'PAGADO') return 'badge badge-ok';
    if (status === 'STOCK_BAJO' || status === 'PENDIENTE' || status === 'ABONADO_PARCIAL')
      return 'badge badge-warn';
    return 'badge badge-danger';
  }

  function filteredAccounts(): CustomerCreditAccount[] {
    const list = snapshot?.credit_portfolio?.accounts || [];
    const q = customerSearch.trim().toLowerCase();
    if (!q) return list;
    return list.filter(
      (a) =>
        a.customer_name.toLowerCase().includes(q) ||
        String(a.customer_id) === q ||
        (a.phone_number && a.phone_number.toLowerCase().includes(q))
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
    refreshAll();
  });
</script>

<div class="layout">
  <!-- Left Navigation Sidebar -->
  <aside class="sidebar">
    <div>
      <div class="brand">
        <div class="brand-icon">AU</div>
        <div>
          <div class="brand-title">Aronium Utility UI</div>
          <div class="brand-sub">Tauri 2 + Svelte + Go CLI</div>
        </div>
      </div>

      <button
        class="nav-btn {activeTab === 'dashboard' ? 'active' : ''}"
        onclick={() => (activeTab = 'dashboard')}
      >
        <span>1. Dashboard & Métricas</span>
        <span>📊</span>
      </button>

      <button
        class="nav-btn {activeTab === 'credit' ? 'active' : ''}"
        onclick={() => (activeTab = 'credit')}
      >
        <span>2. Cuentas de Clientes</span>
        <span>🧾</span>
      </button>

      <button
        class="nav-btn {activeTab === 'inventory' ? 'active' : ''}"
        onclick={() => {
          activeTab = 'inventory';
          loadKardex();
        }}
      >
        <span>3. Inventario & Kardex</span>
        <span>📦</span>
      </button>

      <button
        class="nav-btn {activeTab === 'registers' ? 'active' : ''}"
        onclick={() => (activeTab = 'registers')}
      >
        <span>4. Cajas Registradoras</span>
        <span>🖥️</span>
      </button>

      <button
        class="nav-btn {activeTab === 'drive' ? 'active' : ''}"
        onclick={() => (activeTab = 'drive')}
      >
        <span>5. Google Drive & Backups</span>
        <span>☁️</span>
      </button>

      <button
        class="nav-btn {activeTab === 'doctor' ? 'active' : ''}"
        onclick={() => (activeTab = 'doctor')}
      >
        <span>6. Diagnóstico & Servicio</span>
        <span>🛡️</span>
      </button>
    </div>

    <div class="card" style="margin-bottom: 0; padding: 14px;">
      <div style="font-size: 11px; color: var(--text-muted); margin-bottom: 6px;">
        MOTOR SIDECAR GO CLI
      </div>
      <div style="font-size: 12.5px; font-weight: 700; color: var(--accent-green);">
        ● {doctor?.healthy ? 'pos.db Conectado (RO)' : 'Verificando pos.db...'}
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

  <!-- Right Main Content Area -->
  <main class="main-area">
    <header class="topbar">
      <div>
        <h1 style="margin: 0; font-size: 22px;">
          {snapshot?.store_name || 'Aronium Utility'} — Panel de Control Local
        </h1>
        <div style="font-size: 12.5px; color: var(--text-muted); margin-top: 4px;">
          Interfaz visual Tauri 2 + Svelte impulsada al 100% por el CLI en Go (<code>aronium-util</code>)
        </div>
      </div>
      <div style="display: flex; gap: 10px;">
        <button class="btn btn-outline" disabled={loading} onclick={refreshAll}>
          {loading ? 'Actualizando...' : 'Actualizar Datos'}
        </button>
        <button class="btn" disabled={loading} onclick={handleSyncDrive}>
          Sincronizar Inventario y Cuentas
        </button>
      </div>
    </header>

    {#if toastMessage}
      <div class="toast {toastError ? 'toast-err' : ''}">
        <span>{toastMessage}</span>
        <button class="btn btn-outline" style="padding: 4px 10px;" onclick={() => (toastMessage = '')}>
          Cerrar
        </button>
      </div>
    {/if}

    <!-- TAB 1: DASHBOARD & METRICS -->
    {#if activeTab === 'dashboard'}
      <div class="kpi-grid">
        <div class="kpi-card">
          <div class="kpi-label">Ventas Totales Consolidadas</div>
          <div class="kpi-value">${(dailyReport?.total_sales || 0).toLocaleString()}</div>
          <div class="kpi-sub">
            {dailyReport?.total_transactions || 0} transacciones · Equivalente: {(
              dailyReport?.total_sales_dual || 0
            ).toFixed(2)}
            {dailyReport?.dual_currency_code || 'EUR'}
          </div>
        </div>

        <div class="kpi-card">
          <div class="kpi-label">Cartera de Crédito Pendiente</div>
          <div class="kpi-value" style="color: var(--accent-amber);">
            ${(snapshot?.credit_portfolio?.total_pending_portfolio || 0).toLocaleString()}
          </div>
          <div class="kpi-sub">
            {snapshot?.credit_portfolio?.active_debtor_count || 0} clientes con saldo ·
            {snapshot?.credit_portfolio?.over_limit_count || 0} con límite excedido
          </div>
        </div>

        <div class="kpi-card">
          <div class="kpi-label">Valorización de Inventario (Venta)</div>
          <div class="kpi-value" style="color: var(--accent-green);">
            ${(snapshot?.inventory_summary?.total_retail_value || 0).toLocaleString()}
          </div>
          <div class="kpi-sub">
            Costo: ${(snapshot?.inventory_summary?.total_cost_value || 0).toLocaleString()} ·
            Productos: {snapshot?.inventory_summary?.total_products || 0}
          </div>
        </div>

        <div class="kpi-card">
          <div class="kpi-label">Alertas de Stock Activas</div>
          <div class="kpi-value" style="color: var(--accent-red);">
            {(snapshot?.inventory_summary?.low_stock_count || 0) +
              (snapshot?.inventory_summary?.out_of_stock_count || 0) +
              (snapshot?.inventory_summary?.negative_stock_count || 0)}
          </div>
          <div class="kpi-sub">
            {snapshot?.inventory_summary?.low_stock_count || 0} stock bajo ·
            {snapshot?.inventory_summary?.out_of_stock_count || 0} agotados
          </div>
        </div>
      </div>

      <div class="panel-grid">
        <!-- Sales by Cash Register Visualization -->
        <div class="card">
          <h3 class="card-title">
            <span>Ventas por Caja Registradora</span>
            <span class="badge badge-ok">{snapshot?.sales_by_register?.length || 0} Cajas</span>
          </h3>
          {#each snapshot?.sales_by_register || [] as reg}
            {@const maxSale = Math.max(
              1,
              ...(snapshot?.sales_by_register?.map((r) => r.total_sales) || [1])
            )}
            <div style="margin-bottom: 14px;">
              <div style="display: flex; justify-content: space-between; font-size: 13px;">
                <strong>{reg.register_id} ({reg.transaction_count} docs)</strong>
                <span>
                  Total: <strong>${reg.total_sales.toLocaleString()}</strong> (Crédito: ${reg.credit_sales.toLocaleString()})
                </span>
              </div>
              <div class="bar-track">
                <div
                  class="bar-fill"
                  style="width: {Math.min(100, (reg.total_sales / maxSale) * 100)}%"
                ></div>
              </div>
            </div>
          {/each}
        </div>

        <!-- Top Products Sold Visualization -->
        <div class="card">
          <h3 class="card-title">
            <span>Top Productos por Ingresos</span>
            <span class="badge badge-ok">Rotación en Vivo</span>
          </h3>
          <table>
            <thead>
              <tr>
                <th>Producto</th>
                <th>Unidades</th>
                <th>Total</th>
              </tr>
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

      <!-- Recent Credit Accounts Preview -->
      <div class="card">
        <h3 class="card-title">
          <span>Cuentas de Crédito Activas por Cliente (Sincronizadas con Google Drive)</span>
          <button class="btn btn-outline" onclick={() => (activeTab = 'credit')}>
            Gestionar Cuentas y Anotaciones →
          </button>
        </h3>
        <table>
          <thead>
            <tr>
              <th>Cliente</th>
              <th>Crédito Histórico</th>
              <th>Total Pagado</th>
              <th>Saldo Pendiente</th>
              <th>Límite Asignado</th>
              <th>Estado</th>
            </tr>
          </thead>
          <tbody>
            {#each snapshot?.credit_portfolio?.accounts || [] as acc}
              <tr>
                <td><strong>{acc.customer_name}</strong></td>
                <td>${acc.total_credit_issued.toLocaleString()}</td>
                <td>${acc.total_payments_paid.toLocaleString()}</td>
                <td><strong>${acc.pending_balance.toLocaleString()}</strong></td>
                <td>${acc.credit_limit.toLocaleString()}</td>
                <td><span class={statusBadgeClass(acc.account_status)}>{acc.account_status}</span></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <!-- TAB 2: CUSTOMER CREDIT ACCOUNTS & NOTES -->
    {#if activeTab === 'credit'}
      <div class="panel-grid">
        <div class="card">
          <h3 class="card-title">Agregar Anotación / Compromiso a Cuenta de Cliente</h3>
          <div class="form-grid">
            <div>
              <label style="font-size: 12px; color: var(--text-muted);">
                Nombre o ID del Cliente
                <input
                  class="input"
                  placeholder="Ej. Pedro Pérez o María Gómez"
                  bind:value={noteCustomerTarget}
                />
              </label>
            </div>
            <div>
              <label style="font-size: 12px; color: var(--text-muted);">
                Fecha Compromiso (Opcional)
                <input class="input" type="date" bind:value={notePromiseDate} />
              </label>
            </div>
          </div>
          <div style="margin-top: 10px;">
            <label style="font-size: 12px; color: var(--text-muted);">
              Anotación de Cuenta (Se almacena en base de estado y se sincroniza en Google Drive)
              <input
                class="input"
                placeholder="Ej. Dejó abono parcial, cancela el resto el viernes"
                bind:value={noteText}
              />
            </label>
          </div>
          <button
            class="btn btn-green"
            style="margin-top: 12px; width: 100%;"
            disabled={loading}
            onclick={handleAddCreditNote}
          >
            Guardar Anotación y Sincronizar Cuenta en Google Drive
          </button>
        </div>

        <div class="card">
          <h3 class="card-title">Configurar Límite de Crédito por Cliente</h3>
          <div class="form-grid">
            <div>
              <label style="font-size: 12px; color: var(--text-muted);">
                ID de Cliente en Aronium
                <input class="input" placeholder="Ej. 2" bind:value={limitCustomerID} />
              </label>
            </div>
            <div>
              <label style="font-size: 12px; color: var(--text-muted);">
                Monto Límite de Crédito
                <input class="input" placeholder="Ej. 50000" bind:value={limitAmount} />
              </label>
            </div>
          </div>
          <button
            class="btn"
            style="margin-top: 14px; width: 100%;"
            disabled={loading}
            onclick={handleSetCreditLimit}
          >
            Actualizar Límite de Crédito
          </button>
        </div>
      </div>

      {#if selectedStatement}
        <div class="card">
          <h3 class="card-title">
            <span>Estado de Cuenta Individual: {selectedStatementCustomer}</span>
            <button class="btn btn-outline" onclick={() => (selectedStatement = '')}>Cerrar</button>
          </h3>
          <pre class="stmt-box">{selectedStatement}</pre>
        </div>
      {/if}

      <div class="card">
        <div class="card-title">
          <span>Listado de Cuentas de Clientes y Facturas Enrutadas</span>
          <input
            class="input"
            style="max-width: 280px;"
            placeholder="Buscar cliente por nombre o teléfono..."
            bind:value={customerSearch}
          />
        </div>

        {#each filteredAccounts() as acc}
          <div
            style="border: 1px solid var(--border); border-radius: 10px; padding: 16px; margin-bottom: 14px; background: var(--bg-secondary);"
          >
            <div style="display: flex; justify-content: space-between; align-items: center;">
              <div>
                <strong style="font-size: 16px;">{acc.customer_name}</strong>
                <span style="color: var(--text-muted); font-size: 12px; margin-left: 8px;">
                  ID #{acc.customer_id} · Tel: {acc.phone_number || 'N/A'} · ID Fiscal: {acc.tax_number || 'N/A'}
                </span>
              </div>
              <div style="display: flex; gap: 8px; align-items: center;">
                <span class={statusBadgeClass(acc.account_status)}>{acc.account_status}</span>
                <button
                  class="btn btn-outline"
                  style="padding: 5px 10px; font-size: 12px;"
                  onclick={() => {
                    noteCustomerTarget = acc.customer_name;
                    limitCustomerID = String(acc.customer_id);
                  }}
                >
                  Seleccionar en Formulario
                </button>
                <button
                  class="btn"
                  style="padding: 5px 10px; font-size: 12px;"
                  onclick={() => handleViewStatement(acc.customer_name)}
                >
                  Estado de Cuenta
                </button>
              </div>
            </div>

            <div
              style="display: flex; gap: 24px; margin-top: 10px; font-size: 13px; color: var(--text-muted);"
            >
              <span>Total Crédito: <strong>${acc.total_credit_issued.toLocaleString()}</strong></span>
              <span>Total Pagado: <strong>${acc.total_payments_paid.toLocaleString()}</strong></span>
              <span style="color: var(--accent-amber);">
                Saldo Pendiente: <strong>${acc.pending_balance.toLocaleString()}</strong>
              </span>
              <span>
                Límite: ${acc.credit_limit.toLocaleString()} (Disponible: ${acc.available_credit.toLocaleString()})
              </span>
            </div>

            {#if acc.account_notes && acc.account_notes.length > 0}
              <div
                style="margin-top: 8px; font-size: 12.5px; color: var(--accent-blue); background: rgba(56,189,248,0.08); padding: 8px 12px; border-radius: 6px;"
              >
                <strong>Anotaciones del Cliente:</strong> {acc.account_notes.join(' | ')}
              </div>
            {/if}

            {#if acc.transactions && acc.transactions.length > 0}
              <table style="margin-top: 12px;">
                <thead>
                  <tr>
                    <th>Fecha</th>
                    <th>Factura #</th>
                    <th>Caja</th>
                    <th>Productos Comprados</th>
                    <th>Total</th>
                    <th>Pagado</th>
                    <th>Saldo</th>
                    <th>Notas</th>
                  </tr>
                </thead>
                <tbody>
                  {#each acc.transactions as tx}
                    <tr>
                      <td>{tx.date}</td>
                      <td><strong>{tx.document_number}</strong></td>
                      <td>{tx.cash_register_id}</td>
                      <td>{tx.items_summary}</td>
                      <td>${tx.total_document.toLocaleString()}</td>
                      <td>${tx.paid_amount.toLocaleString()}</td>
                      <td><strong>${tx.pending_balance.toLocaleString()}</strong></td>
                      <td>{tx.combined_notes || '-'}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {/if}
          </div>
        {/each}
      </div>
    {/if}

    <!-- TAB 3: INVENTORY, ALERTS & KARDEX -->
    {#if activeTab === 'inventory'}
      <div class="card">
        <div class="card-title">
          <div style="display: flex; gap: 10px;">
            <button
              class="btn {invSubView === 'catalog' ? '' : 'btn-outline'}"
              onclick={() => (invSubView = 'catalog')}
            >
              Catálogo y Existencias en Vivo ({snapshot?.inventory_summary?.total_products || 0})
            </button>
            <button
              class="btn {invSubView === 'kardex' ? '' : 'btn-outline'}"
              onclick={() => {
                invSubView = 'kardex';
                loadKardex();
              }}
            >
              Kardex de Movimientos ({kardexList.length})
            </button>
          </div>
          <div style="display: flex; gap: 10px;">
            <select class="select" style="width: 170px;" bind:value={invStatusFilter}>
              <option value="ALL">Todos los estados</option>
              <option value="OK">Stock OK</option>
              <option value="STOCK_BAJO">Stock Bajo</option>
              <option value="AGOTADO">Agotados</option>
              <option value="STOCK_NEGATIVO">Stock Negativo</option>
            </select>
            <input
              class="input"
              style="width: 240px;"
              placeholder="Buscar SKU, código o producto..."
              bind:value={invSearch}
            />
          </div>
        </div>

        {#if invSubView === 'catalog'}
          <table>
            <thead>
              <tr>
                <th>SKU / Código</th>
                <th>Código de Barras</th>
                <th>Producto</th>
                <th>Categoría</th>
                <th>Stock Actual</th>
                <th>Mínimo</th>
                <th>Costo Unit.</th>
                <th>Precio Venta</th>
                <th>Margen</th>
                <th>Estado</th>
              </tr>
            </thead>
            <tbody>
              {#each filteredInventory() as item}
                <tr>
                  <td><code>{item.code}</code></td>
                  <td><code>{item.barcode || '-'}</code></td>
                  <td><strong>{item.name}</strong></td>
                  <td>{item.category}</td>
                  <td><strong>{item.stock_quantity} {item.measurement_unit}</strong></td>
                  <td>{item.min_stock}</td>
                  <td>${item.cost.toLocaleString()}</td>
                  <td>${item.sale_price.toLocaleString()}</td>
                  <td>{item.estimated_margin_pct.toFixed(1)}%</td>
                  <td><span class={statusBadgeClass(item.status)}>{item.status}</span></td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <table>
            <thead>
              <tr>
                <th>Fecha</th>
                <th>Documento</th>
                <th>Caja</th>
                <th>Código</th>
                <th>Producto</th>
                <th>Movimiento (Delta)</th>
                <th>Stock Resultante</th>
              </tr>
            </thead>
            <tbody>
              {#each kardexList as k}
                <tr>
                  <td>{k.recorded_at}</td>
                  <td><strong>{k.document_number}</strong></td>
                  <td>{k.register_id}</td>
                  <td><code>{k.product_code}</code></td>
                  <td>{k.product_name}</td>
                  <td style="color: {k.delta_qty < 0 ? 'var(--accent-amber)' : 'var(--accent-green)'};">
                    <strong>{k.delta_qty > 0 ? `+${k.delta_qty}` : k.delta_qty}</strong>
                  </td>
                  <td><strong>{k.resulting_qty}</strong></td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </div>
    {/if}

    <!-- TAB 4: CASH REGISTERS -->
    {#if activeTab === 'registers'}
      <div class="card">
        <h3 class="card-title">Comparativa y Trazabilidad por Caja Registradora (PRO-FR-004)</h3>
        <table>
          <thead>
            <tr>
              <th>ID Caja</th>
              <th>Transacciones</th>
              <th>Ventas Contado</th>
              <th>Ventas a Crédito</th>
              <th>Total Ventas</th>
              <th>Ticket Promedio</th>
            </tr>
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

    <!-- TAB 5: GOOGLE DRIVE & HOT BACKUPS -->
    {#if activeTab === 'drive'}
      <div class="panel-grid">
        <div class="card">
          <h3 class="card-title">
            <span>Conexión Directa con Google Drive & Google Sheets</span>
            <span class={driveStatus?.connected ? 'badge badge-ok' : 'badge badge-warn'}>
              {driveStatus?.connected ? 'OAuth 2.0 Conectado' : 'Modo Carpeta Local / Sin Token'}
            </span>
          </h3>
          <p style="font-size: 13px; color: var(--text-muted); line-height: 1.5;">
            El CLI sincroniza automáticamente la tabla maestra
            <code>{driveStatus?.master_workbook_name || 'Aronium_Inventario_y_Cuentas_Credito.xlsx'}</code>
            y las fichas individuales de cada cliente en
            <code>{driveStatus?.sync_dir || 'GoogleDriveSync'}</code>.
          </p>
          <div style="display: flex; gap: 10px; margin-top: 14px;">
            <button class="btn" disabled={loading} onclick={handleDriveWebLogin}>
              Autenticar Google Drive vía Web (OAuth 2.0)
            </button>
            <button class="btn btn-green" disabled={loading} onclick={handleSyncDrive}>
              Sincronizar Tabla Ahora
            </button>
          </div>

          {#if lastSyncResult}
            <div
              style="margin-top: 14px; padding: 12px; border-radius: 8px; background: var(--bg-primary); font-size: 12.5px;"
            >
              <div><strong>Última Tabla Maestra Generada:</strong> {lastSyncResult.master_workbook_path}</div>
              <div style="margin-top: 4px;">
                <strong>Cuentas de Clientes Actualizadas:</strong>
                {lastSyncResult.customer_files?.length || 0} archivos
              </div>
            </div>
          {/if}
        </div>

        <div class="card">
          <h3 class="card-title">Respaldo en Caliente de pos.db (SQLite Online Snapshot)</h3>
          <p style="font-size: 13px; color: var(--text-muted); line-height: 1.5;">
            Crea un snapshot transaccionalmente consistente (<code>VACUUM INTO</code>) sin cerrar Aronium Lite,
            comprimido en <code>.zip</code> y con cifrado opcional <strong>AES-256-GCM</strong>.
          </p>
          <label style="font-size: 12px; color: var(--text-muted);">
            Frase de Cifrado AES-256-GCM (Opcional)
            <input
              class="input"
              type="password"
              placeholder="Dejar vacío para respaldo comprimido estándar"
              bind:value={backupPassphrase}
            />
          </label>
          <button
            class="btn"
            style="width: 100%; margin-top: 12px;"
            disabled={loading}
            onclick={handleCreateBackup}
          >
            Crear Respaldo en Caliente Ahora
          </button>

          {#if lastBackup}
            <div
              style="margin-top: 14px; padding: 12px; border-radius: 8px; background: var(--bg-primary); font-size: 12px;"
            >
              <div><strong>Archivo:</strong> {lastBackup.archive_path}</div>
              <div><strong>SHA-256:</strong> <code>{lastBackup.sha256}</code></div>
            </div>
          {/if}
        </div>
      </div>
    {/if}

    <!-- TAB 6: DOCTOR & AUTO-START SERVICE -->
    {#if activeTab === 'doctor'}
      <div class="panel-grid">
        <div class="card">
          <h3 class="card-title">
            <span>Diagnóstico de Salud del Sistema (`aronium-util doctor`)</span>
            <span class={doctor?.healthy ? 'badge badge-ok' : 'badge badge-danger'}>
              {doctor?.healthy ? 'SALUDABLE' : 'REVISAR'}
            </span>
          </h3>
          <table>
            <tbody>
              <tr>
                <td><strong>Base Aronium (Solo Lectura)</strong></td>
                <td><code>{doctor?.pos_db_path}</code> ({doctor?.pos_db_readable ? 'OK' : 'Error'})</td>
              </tr>
              <tr>
                <td><strong>PRAGMA quick_check</strong></td>
                <td><span class="badge badge-ok">{doctor?.quick_check_result}</span></td>
              </tr>
              <tr>
                <td><strong>Versión Esquema Aronium</strong></td>
                <td>{doctor?.aronium_schema_version} ({doctor?.detected_tables?.length || 0} tablas)</td>
              </tr>
              <tr>
                <td><strong>Base de Estado Local</strong></td>
                <td><code>{doctor?.state_db_path}</code> ({doctor?.state_db_ready ? 'Lista' : 'Error'})</td>
              </tr>
              <tr>
                <td><strong>Identidad de Caja</strong></td>
                <td>
                  <strong>{doctor?.cash_register_id}</strong> (Conflicto en red: {doctor?.register_id_conflict
                    ? 'SÍ'
                    : 'No'})
                </td>
              </tr>
              <tr>
                <td><strong>Cola Offline (sync_outbox)</strong></td>
                <td>{doctor?.pending_outbox_count || 0} eventos pendientes</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="card">
          <h3 class="card-title">Servicio de Inicio Automático con Windows (`aronium-util watch`)</h3>
          <p style="font-size: 13px; color: var(--text-muted); line-height: 1.5;">
            El motor CLI se registra en el arranque de Windows para ejecutarse apenas se encienda el equipo y
            procesar en vivo todas las ventas, cambios de stock y cuentas de crédito que se registren en
            <code>pos.db</code>.
          </p>
          <div
            style="padding: 12px; border-radius: 8px; background: var(--bg-primary); font-size: 12.5px; margin-bottom: 14px;"
          >
            <strong>Estado actual:</strong> <code>{serviceStatus?.status || 'Consultando...'}</code>
          </div>
          <button
            class="btn btn-green"
            style="width: 100%;"
            disabled={loading}
            onclick={handleInstallAutoStartService}
          >
            Registrar / Actualizar Inicio Automático al Encender Windows
          </button>
        </div>
      </div>
    {/if}
  </main>
</div>
