import { useI18n } from '../../i18n'
import { useAccountsData } from './useAccountsData'
import { useAccountActions } from './useAccountActions'
import { useDeviceIdPool } from './useDeviceIdPool'
import QueueCards from './QueueCards'
import ApiKeysPanel from './ApiKeysPanel'
import AccountsTable from './AccountsTable'
import AddKeyModal from './AddKeyModal'
import AddAccountModal from './AddAccountModal'
import EditAccountModal from './EditAccountModal'
import ElasticPoolModal from './ElasticPoolModal'
import DeviceIdModal from './DeviceIdModal'

export default function AccountManagerContainer({ config, onRefresh, onMessage, authFetch }) {
    const { t } = useI18n()
    const apiFetch = authFetch || fetch

    const {
        queueStatus,
        keysExpanded,
        setKeysExpanded,
        accounts,
        page,
        pageSize,
        totalPages,
        totalAccounts,
        loadingAccounts,
        fetchAccounts,
        changePageSize,
        resolveAccountIdentifier,
        searchQuery,
        handleSearchChange,
    } = useAccountsData({ apiFetch, onConfigRefresh: onRefresh })

    const {
        showAddKey,
        openAddKey,
        openEditKey,
        closeKeyModal,
        editingKey,
        showAddAccount,
        openAddAccount,
        closeAddAccount,
        showEditAccount,
        editingAccount,
        editAccount,
        setEditAccount,
        openEditAccount,
        closeEditAccount,
        newKey,
        setNewKey,
        copiedKey,
        setCopiedKey,
        newAccount,
        setNewAccount,
        loading,
        testing,
        testingAll,
        batchProgress,
        sessionCounts,
        deletingSessions,
        updatingProxy,
        togglingEnabled,
        togglingAllEnabled,
        deletingBanned,
        addKey,
        deleteKey,
        addAccount,
        updateAccount,
        deleteAccount,
        testAccount,
        testAllAccounts,
        deleteAllSessions,
        updateAccountProxy,
        toggleAccountEnabled,
        toggleAllAccountsEnabled,
        deleteBannedAccounts,
        showElasticPool,
        openElasticPool,
        closeElasticPool,
        elasticPool,
        setElasticPool,
        savingElasticPool,
        saveElasticPool,
    } = useAccountActions({
        apiFetch,
        t,
        onMessage,
        onRefresh,
        config,
        fetchAccounts,
        resolveAccountIdentifier,
    })

    const {
        showDeviceIds,
        openDeviceIds,
        closeDeviceIds,
        deviceIdItems,
        addingDeviceId,
        deletingDeviceId,
        addDeviceId,
        deleteDeviceId,
    } = useDeviceIdPool({ apiFetch, t, onMessage, onRefresh, fetchAccounts })

    const deviceIdMode = queueStatus?.device_id_mode || config?.runtime?.device_id_mode || 'manual'
    // 队列状态是轮询获取的，未拿到前传 undefined，避免误报"数量不足"。
    const deviceIdRemaining = queueStatus?.device_id_remaining

    return (
        <div className="space-y-6">
            {Boolean(config?.env_source_present) && (
                <div className={`rounded-xl border px-4 py-3 text-sm ${
                    config?.env_writeback_enabled
                        ? (config?.env_backed ? 'border-amber-500/30 bg-amber-500/10 text-amber-600' : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-600')
                        : 'border-amber-500/30 bg-amber-500/10 text-amber-600'
                }`}>
                    <p className="font-medium">
                        {config?.env_writeback_enabled
                            ? (config?.env_backed
                                ? t('accountManager.envModeWritebackPendingTitle')
                                : t('accountManager.envModeWritebackActiveTitle'))
                            : t('accountManager.envModeRiskTitle')}
                    </p>
                    <p className="mt-1 text-xs opacity-90">
                        {config?.env_writeback_enabled
                            ? t('accountManager.envModeWritebackDesc', { path: config?.config_path || 'config.json' })
                            : t('accountManager.envModeRiskDesc')}
                    </p>
                </div>
            )}

            <QueueCards queueStatus={queueStatus} t={t} />

            <ApiKeysPanel
                t={t}
                config={config}
                keysExpanded={keysExpanded}
                setKeysExpanded={setKeysExpanded}
                onAddKey={openAddKey}
                onEditKey={openEditKey}
                copiedKey={copiedKey}
                setCopiedKey={setCopiedKey}
                onDeleteKey={deleteKey}
            />

            <AccountsTable
                t={t}
                accounts={accounts}
                loadingAccounts={loadingAccounts}
                testing={testing}
                testingAll={testingAll}
                batchProgress={batchProgress}
                sessionCounts={sessionCounts}
                deletingSessions={deletingSessions}
                updatingProxy={updatingProxy}
                togglingEnabled={togglingEnabled}
                togglingAllEnabled={togglingAllEnabled}
                deletingBanned={deletingBanned}
                totalAccounts={totalAccounts}
                page={page}
                pageSize={pageSize}
                totalPages={totalPages}
                resolveAccountIdentifier={resolveAccountIdentifier}
                proxies={config?.proxies || []}
                elasticPoolEnabled={Boolean(config?.elastic_pool?.enabled)}
                onOpenElasticPool={openElasticPool}
                onTestAll={testAllAccounts}
                onShowAddAccount={openAddAccount}
                onEditAccount={openEditAccount}
                onTestAccount={testAccount}
                onDeleteAccount={deleteAccount}
                onDeleteAllSessions={deleteAllSessions}
                onUpdateAccountProxy={updateAccountProxy}
                onToggleAccountEnabled={toggleAccountEnabled}
                onToggleAllAccountsEnabled={toggleAllAccountsEnabled}
                onDeleteBannedAccounts={deleteBannedAccounts}
                onPrevPage={() => fetchAccounts(page - 1)}
                onNextPage={() => fetchAccounts(page + 1)}
                onPageSizeChange={changePageSize}
                searchQuery={searchQuery}
                onSearchChange={handleSearchChange}
                envBacked={Boolean(config?.env_backed)}
                deviceIdMode={deviceIdMode}
                deviceIdRemaining={deviceIdRemaining}
                onOpenDeviceIds={openDeviceIds}
            />

            <AddKeyModal
                show={showAddKey}
                t={t}
                editingKey={editingKey}
                newKey={newKey}
                setNewKey={setNewKey}
                loading={loading}
                onClose={closeKeyModal}
                onAdd={addKey}
            />

            <AddAccountModal
                show={showAddAccount}
                t={t}
                newAccount={newAccount}
                setNewAccount={setNewAccount}
                loading={loading}
                onClose={closeAddAccount}
                onAdd={addAccount}
            />

            <EditAccountModal
                show={showEditAccount}
                t={t}
                editingAccount={editingAccount}
                editAccount={editAccount}
                setEditAccount={setEditAccount}
                loading={loading}
                onClose={closeEditAccount}
                onSave={updateAccount}
            />

            <ElasticPoolModal
                show={showElasticPool}
                t={t}
                elasticPool={elasticPool}
                setElasticPool={setElasticPool}
                loading={savingElasticPool}
                onClose={closeElasticPool}
                onSave={saveElasticPool}
            />

            <DeviceIdModal
                show={showDeviceIds}
                t={t}
                items={deviceIdItems}
                loading={addingDeviceId}
                deleting={deletingDeviceId}
                onClose={closeDeviceIds}
                onAdd={addDeviceId}
                onDelete={deleteDeviceId}
            />
        </div>
    )
}
