"use client";

import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { AgentCommandRecord, AgentExecutionStatus, AgentStatus, DashboardSnapshot, ExecutionBindingRecord } from "../lib/app-types";
import { agentAction, agentStatusLabel } from "../lib/agent-lifecycle";
import { depositCalls, mandateCall, parseTokenAmount, withdrawalCall } from "../lib/strategy-calls";
import { formatAddress, formatAmount } from "../lib/format";
import { robinhoodMainnetExplorer, robinhoodMainnetUSDG } from "../lib/chain";
import { canonicalDeploymentAction, canonicalOwnerActionSet } from "../lib/mainnet-actions";
import { useAppApi, useRobinAuth, useSmartWallet } from "./app-providers";
import { ErrorNotice } from "./app-ui";

export function AgentButton({ dashboard }: { dashboard: DashboardSnapshot }) {
  const api = useAppApi();
  const queryClient = useQueryClient();
  const agent = dashboard.agent;
  const action = agentAction(agent);
  const launchRequiresReadiness = action?.kind === "command" && action.command === "launch";
  const pendingCommand = action?.kind === "command"
    ? action.command
    : agent?.status === "reducing"
      ? "pause"
      : agent?.status === "closing"
        ? "close"
        : null;
  const [commandId, setCommandId] = useState<string | null>(null);
  useEffect(() => {
    if (!agent) {
      setCommandId(null);
      return;
    }
    if (commandId || !pendingCommand) return;
    setCommandId(api.pendingAgentCommand(agent.id, pendingCommand));
  }, [agent?.id, api, commandId, pendingCommand]);
  const transitioning = agent?.status === "reducing" || agent?.status === "closing";
  const recoverableCommand = action?.kind === "command" || transitioning;
  const activeCommand = useQuery({
    queryKey: ["agent-command-pending", agent?.id],
    queryFn: () => api.activeAgentCommand(agent!.id),
    enabled: Boolean(agent && recoverableCommand && !commandId),
    retry: false,
    refetchInterval: transitioning && !commandId ? 2_000 : false,
  });
  useEffect(() => {
    if (activeCommand.data?.id) {
      setCommandId(activeCommand.data.id);
      return;
    }
    if (activeCommand.data === null && transitioning) {
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    }
  }, [activeCommand.data, queryClient, transitioning]);
  const commandStatus = useQuery({
    queryKey: ["agent-command", agent?.id, commandId],
    queryFn: () => api.getAgentCommand(agent!.id, commandId!),
    enabled: Boolean(agent && commandId),
    refetchInterval: (query) => terminalCommand(query.state.data?.status) ? false : 2_000,
  });
  const launchReadiness = useQuery({
    queryKey: ["agent-readiness", agent?.id],
    queryFn: () => api.agentReadiness(agent!.id),
    enabled: Boolean(agent && launchRequiresReadiness),
    retry: false,
    refetchInterval: launchRequiresReadiness ? 2_000 : false,
  });
  useEffect(() => {
    if (!terminalCommand(commandStatus.data?.status)) return;
    queryClient.setQueryData(["agent-command-pending", agent?.id], null);
    setCommandId(null);
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    void queryClient.invalidateQueries({ queryKey: ["agent-execution"] });
    void queryClient.invalidateQueries({ queryKey: ["agent-command-pending", agent?.id] });
  }, [agent?.id, commandStatus.data?.status, queryClient]);
  const mutation = useMutation<unknown, Error>({
    mutationKey: ["agent-command-mutation", agent?.id],
    mutationFn: () => {
      if (!action) throw new Error("This lifecycle state has no manual transition.");
      if (action.kind === "create") return api.launchAgent();
      if (!agent) throw new Error("Agent is not available.");
      if (action.kind === "provision") return api.createExecutionAccount(agent.id);
      if (action.kind === "paper") return api.updatePaperAgent(agent.id, action.status);
      return api.agentCommand(agent.id, action.command);
    },
    onSuccess: (result) => {
      if (action?.kind === "command") {
        const command = result as AgentCommandRecord;
        if (!terminalCommand(command.status)) {
          queryClient.setQueryData(["agent-command-pending", agent?.id], command);
          setCommandId(command.id);
        }
      }
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-execution"] });
    },
    onError: () => {
      void queryClient.invalidateQueries({ queryKey: ["agent-command-pending", agent?.id] });
    },
  });

  if (!action) return null;
  const launchReady = !launchRequiresReadiness || launchReadiness.data?.canLaunch === true;
  const checkingCommand = action.kind === "command" && activeCommand.isLoading;
  const commandInFlight = Boolean(commandId || activeCommand.data);
  return (
    <div className="inline-action">
      <button className="button button-primary" disabled={mutation.isPending || commandInFlight || checkingCommand || !launchReady} onClick={() => mutation.mutate()}>
        {mutation.isPending ? "Updating…" : commandInFlight ? "Command pending…" : checkingCommand ? "Checking command…" : launchRequiresReadiness && !launchReady ? "Waiting for readiness…" : action.label}
      </button>
      {commandStatus.data && !terminalCommand(commandStatus.data.status) && <small aria-live="polite">{commandStatus.data.command}: {commandStatus.data.status}</small>}
      {commandStatus.data?.errorReason && <span className="field-error" role="alert">{commandStatus.data.errorReason}</span>}
      {mutation.error && <span className="field-error" role="alert">{mutation.error.message}</span>}
      {commandStatus.error && <><span className="field-error" role="alert">{commandStatus.error.message}</span><button className="button button-quiet" onClick={() => void commandStatus.refetch()}>Retry command status</button></>}
      {launchReadiness.error && <span className="field-error" role="alert">{launchReadiness.error.message}</span>}
    </div>
  );
}

export function LiveExecutionPanel({ dashboard }: { dashboard: DashboardSnapshot }) {
  const api = useAppApi();
  const agent = dashboard.agent;
  const enabled = agent?.mode === "live" && canReadLiveExecution(agent.status);
  const execution = useQuery({
    queryKey: ["agent-execution", agent?.id],
    queryFn: () => api.agentExecution(agent!.id),
    enabled,
    retry: false,
    refetchInterval: enabled && agent?.status !== "closed" ? 2_000 : false,
  });

  if (!enabled) return null;
  if (execution.error) return <ErrorNotice error={execution.error} retry={() => void execution.refetch()} />;
  if (!execution.data) return null;
  return <section className="panel"><ExecutionTelemetry execution={execution.data} /></section>;
}

export function MainnetReadinessPanel({ dashboard }: { dashboard: DashboardSnapshot }) {
  const api = useAppApi();
  const auth = useRobinAuth();
  const smartWallet = useSmartWallet();
  const queryClient = useQueryClient();
  const agent = dashboard.agent;
  const agentCommandMutations = useIsMutating({ mutationKey: ["agent-command-mutation", agent?.id] });
  const [lighterBinding, setLighterBinding] = useState<ExecutionBindingRecord | null>(null);
  const [robinhoodBinding, setRobinhoodBinding] = useState<ExecutionBindingRecord | null>(null);
  const [robinhoodTransactionHash, setRobinhoodTransactionHash] = useState<string | null>(null);
  const [lifecycleCommand, setLifecycleCommand] = useState<AgentCommandRecord | null>(null);
  const [recoveredLifecycleCommandId, setRecoveredLifecycleCommandId] = useState<string | null>(null);
  const [submittedOwnerActions, setSubmittedOwnerActions] = useState<string[]>([]);
  const [completedOwnerTransactions, setCompletedOwnerTransactions] = useState<string[]>([]);
  const [robinhoodDepositAmount, setRobinhoodDepositAmount] = useState("");
  const [lighterOwner, setLighterOwner] = useState<string>(auth.embeddedAddress ?? auth.accounts[0]?.address ?? "");
  useEffect(() => {
    setLifecycleCommand(null);
    setRecoveredLifecycleCommandId(agent
      ? api.pendingAgentCommand(agent.id, "withdraw") ?? api.pendingAgentCommand(agent.id, "close")
      : null);
    setCompletedOwnerTransactions(agent ? readTransactionHashes(completedOwnerStorageKey(agent.id)) : []);
  }, [agent?.id, api]);
  useEffect(() => {
    if (!lighterOwner && auth.accounts.length) setLighterOwner(auth.accounts[0].address);
  }, [auth.accounts, lighterOwner]);
  const hasAccount = agent?.mode === "live" && agent.status !== "setup";
  const readiness = useQuery({
    queryKey: ["agent-readiness", agent?.id],
    queryFn: () => api.agentReadiness(agent!.id),
    enabled: hasAccount,
    retry: false,
    refetchInterval: hasAccount && agent?.status !== "closed" ? 3_000 : false,
  });
  const execution = useQuery({
    queryKey: ["agent-execution", agent?.id],
    queryFn: () => api.agentExecution(agent!.id),
    enabled: hasAccount && readiness.data?.coordinatorRegistered === true,
    retry: false,
    refetchInterval: hasAccount && agent?.status !== "closed" ? 2_000 : false,
  });
  useEffect(() => {
    if (readiness.data?.canLaunch) {
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    }
  }, [queryClient, readiness.data?.canLaunch]);
  const mainnetDeposit = useMutation({
    mutationFn: async () => {
      const current = readiness.data;
      if (!current?.robinhoodOwnerAddress || !current.robinhoodVaultAddress) {
        throw new Error("The verified Robinhood owner and vault are not ready.");
      }
      const owner = requireAddress(current.robinhoodOwnerAddress);
      const vault = requireAddress(current.robinhoodVaultAddress);
      const amount = parseTokenAmount(robinhoodDepositAmount, 6);
      for (const call of depositCalls(robinhoodMainnetUSDG, vault, amount)) {
        await smartWallet.executeMainnetCall(call, owner);
      }
    },
    onSuccess: () => {
      setRobinhoodDepositAmount("");
      void readiness.refetch();
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
  const lighter = useMutation({
    mutationFn: () => {
      if (!agent || !lighterOwner) throw new Error("Link an execution wallet first.");
      return api.requestLighterLink(agent.id, { ownerAddress: lighterOwner });
    },
    onSuccess: (binding) => {
      setLighterBinding(binding);
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
  const lighterConfirm = useMutation({
    mutationFn: async () => {
      if (!agent || !lighterBinding?.providerRequestId || !lighterBinding.associationPayload) {
        throw new Error("The Lighter association payload is not ready.");
      }
      const signature = await auth.signMessage(lighterBinding.associationPayload, lighterBinding.ownerAddress);
      return api.confirmLighterLink(agent.id, {
        requestId: lighterBinding.requestId,
        linkId: lighterBinding.providerRequestId,
        l1Signature: signature,
      });
    },
    onSuccess: (binding) => {
      setLighterBinding(binding);
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-execution"] });
    },
  });
  const robinhood = useMutation({
    mutationFn: () => {
      if (!agent) throw new Error("Create the agent first.");
      return api.prepareRobinhood(agent.id);
    },
    onSuccess: (binding) => {
      setRobinhoodBinding(binding);
      setRobinhoodTransactionHash(readTransactionHashes(deploymentStorageKey(binding.requestId))[0] ?? null);
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
  const robinhoodDeploy = useMutation({
    mutationFn: async () => {
      if (!agent || !robinhoodBinding?.robinhoodDeploymentAction) {
        throw new Error("Prepare the Robinhood deployment first.");
      }
      const action = robinhoodBinding.robinhoodDeploymentAction;
      if (!canonicalDeploymentAction(robinhoodBinding)) {
        throw new Error("The prepared Robinhood deployment is invalid.");
      }
      const key = deploymentStorageKey(robinhoodBinding.requestId);
      const hashes = readTransactionHashes(key);
      const actionIndex = action.kind === "deploy_user_graph" ? 0 : 1;
      if (!hashes[actionIndex]) {
        await smartWallet.executeMainnetCall(action, robinhoodBinding.ownerAddress, (submitted) => {
          hashes[actionIndex] = submitted;
          window.localStorage.setItem(key, JSON.stringify(hashes));
          setRobinhoodTransactionHash(submitted);
        });
      }
      const transactionHash = hashes[actionIndex];
      if (!transactionHash) throw new Error("The verified deployment transaction is unavailable.");
      const binding = await api.confirmRobinhood(agent.id, {
        requestId: robinhoodBinding.requestId,
        transactionHash,
      });
      if (binding.status === "linked") window.localStorage.removeItem(key);
      return binding;
    },
    onSuccess: (binding) => {
      setRobinhoodBinding(binding);
      setRobinhoodTransactionHash(binding.status === "linked" ? null : binding.proofTransactionHash);
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    },
  });
  const lifecycle = useMutation({
    mutationKey: ["agent-command-mutation", agent?.id],
    mutationFn: (command: "close" | "withdraw") => {
      if (!agent) throw new Error("Create the agent first.");
      return api.agentCommand(agent.id, command);
    },
    onSuccess: (command) => {
      setRecoveredLifecycleCommandId(null);
      setLifecycleCommand(command);
      if (command.command === "withdraw" && agent) {
        removeStoredTransactions(completedOwnerStorageKey(agent.id));
        setCompletedOwnerTransactions([]);
      }
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    },
  });
  const lifecycleCommandId = lifecycleCommand?.id ?? recoveredLifecycleCommandId;
  const activeLifecycleCommand = useQuery({
    queryKey: ["agent-command-pending", agent?.id],
    queryFn: () => api.activeAgentCommand(agent!.id),
    enabled: Boolean(agent && !lifecycleCommandId),
    retry: false,
    refetchInterval: agent?.status === "closing" && !lifecycleCommandId ? 2_000 : false,
  });
  useEffect(() => {
    if (activeLifecycleCommand.data?.id && matchesLifecyclePanelCommand(activeLifecycleCommand.data.command)) {
      setRecoveredLifecycleCommandId(activeLifecycleCommand.data.id);
    }
  }, [activeLifecycleCommand.data]);
  const commandStatus = useQuery({
    queryKey: ["agent-command", agent?.id, lifecycleCommandId],
    queryFn: () => api.getAgentCommand(agent!.id, lifecycleCommandId!),
    enabled: Boolean(agent && lifecycleCommandId && !terminalCommand(lifecycleCommand?.status)),
    refetchInterval: (query) => terminalCommand(query.state.data?.status) ? false : 2_000,
  });
  const currentCommand = commandStatus.data ?? lifecycleCommand ?? activeLifecycleCommand.data ?? undefined;
  useEffect(() => {
    if (!currentCommand) {
      setSubmittedOwnerActions([]);
      return;
    }
    const key = ownerActionStorageKey(currentCommand.id);
    const hashes = readTransactionHashes(key);
    if (terminalCommand(currentCommand.status)) {
      if (recoveredLifecycleCommandId === currentCommand.id) {
        setLifecycleCommand(currentCommand);
        setRecoveredLifecycleCommandId(null);
      }
      if (currentCommand.command === "withdraw" && currentCommand.status === "completed" && agent && hashes.length) {
        setCompletedOwnerTransactions(hashes);
        storeTransactionHashes(completedOwnerStorageKey(agent.id), hashes);
      }
      removeStoredTransactions(key);
      setSubmittedOwnerActions([]);
      return;
    }
    setSubmittedOwnerActions(hashes);
  }, [agent?.id, currentCommand, recoveredLifecycleCommandId]);
  useEffect(() => {
    if (!terminalCommand(currentCommand?.status)) return;
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    void queryClient.invalidateQueries({ queryKey: ["agent-execution"] });
  }, [currentCommand?.status, queryClient]);
  const ownerAction = useMutation({
    mutationFn: async () => {
      if (!currentCommand || currentCommand.status !== "awaiting_signature" || !currentCommand.ownerActions.length) {
        throw new Error("The reconciled withdrawal action is not ready.");
      }
      if (!state?.robinhoodOwnerAddress || !state.robinhoodVaultAddress) {
        throw new Error("The canonical Robinhood owner and vault are unavailable.");
      }
      if (!canonicalOwnerActionSet(currentCommand.ownerActions, state.robinhoodOwnerAddress, state.robinhoodVaultAddress)) {
        throw new Error("The prepared withdrawal actions do not match the canonical vault.");
      }
      const key = ownerActionStorageKey(currentCommand.id);
      const hashes = readTransactionHashes(key);
      for (const [index, action] of currentCommand.ownerActions.entries()) {
        if (hashes[index]) continue;
        await smartWallet.executeMainnetCall(
          { to: action.to, data: action.data, value: action.value },
          action.from,
          (submitted) => {
            hashes[index] = submitted;
            window.localStorage.setItem(key, JSON.stringify(hashes));
            setSubmittedOwnerActions([...hashes]);
          },
        );
      }
    },
    onSuccess: () => {
      void commandStatus.refetch();
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    },
  });

  if (!agent || agent.mode !== "live") return null;
  const state = readiness.data;
  const readinessStatus = readinessBadge(agent.status, state?.canLaunch === true);
  const canProvision = matchesProvisioning(agent.status);
  const withdrawalInFlight = currentCommand?.command === "withdraw" && !terminalCommand(currentCommand.status);
  const lifecycleInFlight = Boolean(currentCommand && !terminalCommand(currentCommand.status));
  const checkingLifecycle = activeLifecycleCommand.isLoading || activeLifecycleCommand.isFetching;
  const requirements = [
    { label: "Execution registration", ready: Boolean(state?.coordinatorRegistered), detail: "Immutable venue and vault bindings registered with the coordinator" },
    { label: "Robinhood USDG", ready: Boolean(state?.robinhoodDeployed && state.robinhoodFunded), detail: "Vault deployed and funded on Robinhood Chain mainnet" },
    { label: "Lighter USDC", ready: Boolean(state?.lighterLinked && state.lighterFunded), detail: "User-owned subaccount linked and collateralized" },
    { label: "User ETH gas", ready: Boolean(state?.userGasReady), detail: "Pays deployment and owner transactions without sponsorship" },
    { label: "Execution ETH gas", ready: Boolean(state?.executionGasReady), detail: "Funds the isolated execution signer" },
    { label: "Policy and reconciliation", ready: Boolean(state?.policyActive && state.reconciled), detail: "Independent services must verify both accounts" },
  ];

  return (
    <section className="panel mainnet-readiness">
      <div className="panel-heading">
        <div><span className="eyebrow">Mainnet execution</span><h2>Agent {agentStatusLabel(agent.status)}</h2></div>
        <span className={`status-pill ${readinessStatus.ready ? "running" : "halted"}`}>{readinessStatus.label}</span>
      </div>
      <small>Strategy: {agent.strategyVersion}</small>
      <p className="readiness-copy">AAPL only, capped at $25 per leg. Each venue is funded separately. Owner-paid transactions use Robinhood Chain ETH.</p>
      <div className="readiness-grid">
        {requirements.map((requirement) => (
          <article key={requirement.label}>
            <span className={`status-dot ${requirement.ready ? "running" : "halted"}`} />
            <div><strong>{requirement.label}</strong><small>{requirement.detail}</small></div>
          </article>
        ))}
      </div>
      {execution.data && <ExecutionTelemetry execution={execution.data} />}
      {state?.validUntil && <small>Readiness evidence valid until {new Date(state.validUntil).toLocaleString()}.</small>}
      {state?.blockers.length ? <small role="status">Blocked by: {state.blockers.map(formatReadinessBlocker).join(", ")}.</small> : null}
      {state?.robinhoodVaultAddress && <small>Robinhood vault: <a href={`${robinhoodMainnetExplorer}/address/${state.robinhoodVaultAddress}`} target="_blank" rel="noreferrer">{formatAddress(state.robinhoodVaultAddress)}</a>.</small>}
      {state?.robinhoodDeployed && !state.robinhoodFunded && <div className="button-row">
        <label>USDG to deposit<input inputMode="decimal" value={robinhoodDepositAmount} onChange={(event) => setRobinhoodDepositAmount(event.target.value)} placeholder="25.00" /></label>
        <button className="button button-primary" disabled={mainnetDeposit.isPending || !robinhoodDepositAmount} onClick={() => mainnetDeposit.mutate()}>{mainnetDeposit.isPending ? "Depositing…" : "Deposit USDG with owner ETH"}</button>
      </div>}
      {state?.lighterLinked && !state.lighterFunded && <small>Fund Lighter account {state.lighterAccountIndex ?? "pending"} with USDC through the user-owned Lighter account. <a href="https://apidocs.lighter.xyz/docs/deposits-transfers-and-withdrawals" target="_blank" rel="noreferrer">Lighter funding instructions ↗</a></small>}
      {state?.robinhoodSignerAddress && !state.executionGasReady && <small>Execution signer ETH address: <a href={`${robinhoodMainnetExplorer}/address/${state.robinhoodSignerAddress}`} target="_blank" rel="noreferrer">{state.robinhoodSignerAddress}</a>.</small>}
      {agent.status === "setup" ? (
        <small>Set up the execution account before linking venues or funding capital.</small>
      ) : (
        <>
          {canProvision && <><div className="button-row">
            <label>Lighter owner<select value={lighterOwner} onChange={(event) => setLighterOwner(event.target.value)}>{auth.accounts.map((account) => <option key={account.address} value={account.address}>{account.label}</option>)}</select></label>
          </div><div className="button-row">
            <button className="button button-secondary" disabled={lighter.isPending} onClick={() => lighter.mutate()}>{lighter.isPending ? "Checking Lighter…" : "Find empty Lighter subaccount"}</button>
            {(lighterBinding?.status === "awaiting_signature" || lighterBinding?.status === "verifying") && lighterBinding.associationPayload && <button className="button button-primary" disabled={lighterConfirm.isPending} onClick={() => lighterConfirm.mutate()}>{lighterConfirm.isPending ? "Verifying…" : lighterBinding.status === "verifying" ? "Retry Lighter verification" : "Sign Lighter association"}</button>}
            <button className="button button-secondary" disabled={robinhood.isPending} onClick={() => robinhood.mutate()}>{robinhood.isPending ? "Preparing…" : "Prepare Robinhood deployment"}</button>
            {robinhoodBinding?.status === "awaiting_signature" && robinhoodBinding.robinhoodDeploymentAction && <button className="button button-primary" disabled={robinhoodDeploy.isPending || smartWallet.pending} onClick={() => robinhoodDeploy.mutate()}>{robinhoodDeploy.isPending ? "Confirming…" : robinhoodBinding.robinhoodDeploymentAction.kind === "authorize_execution_agent" ? "Authorize execution agent" : robinhoodTransactionHash ? "Retry finality check" : "Deploy with owner ETH"}</button>}
          </div><small>Robin finds a new empty non-master subaccount owned by this wallet. If none is available, <a href="https://app.lighter.xyz/" target="_blank" rel="noreferrer">create an empty subaccount in Lighter ↗</a>, then retry.</small></>}
          <div className="button-row">
            {!matchesTerminal(agent.status) && !lifecycleInFlight && !checkingLifecycle && <button className="button button-quiet danger" disabled={lifecycle.isPending || agentCommandMutations > 0} onClick={() => lifecycle.mutate("close")}>Close agent</button>}
            {agent.status === "closed" && !lifecycleInFlight && !checkingLifecycle && !withdrawalInFlight && <button className="button button-secondary" disabled={lifecycle.isPending || agentCommandMutations > 0 || !state?.reconciled} onClick={() => lifecycle.mutate("withdraw")}>Prepare owner withdrawal</button>}
          </div>
          {currentCommand?.status === "awaiting_signature" && currentCommand.ownerActions.length > 0 && <button className="button button-primary" disabled={ownerAction.isPending || smartWallet.pending || submittedOwnerActions.length === currentCommand.ownerActions.length} onClick={() => ownerAction.mutate()}>{ownerAction.isPending ? "Submitting…" : submittedOwnerActions.length === currentCommand.ownerActions.length ? "Awaiting reconciliation" : "Sign owner withdrawal"}</button>}
          {currentCommand?.command === "withdraw" && currentCommand.status === "completed" && <small role="status">Owner withdrawal completed.</small>}
          {completedOwnerTransactions.map((hash) => <small key={hash}><a href={`${robinhoodMainnetExplorer}/tx/${hash}`} target="_blank" rel="noreferrer">Submitted owner transaction {formatAddress(hash)} ↗</a></small>)}
        </>
      )}
      {lighterBinding && <small>Lighter request {lighterBinding.requestId}: {lighterBinding.status}. The user-owned L1 wallet must sign the association payload before verification can complete.</small>}
      {robinhoodBinding && <small>Robinhood request {robinhoodBinding.requestId}: {robinhoodBinding.status}. Deployment and deposit remain owner-controlled transactions.</small>}
      <small>The product API stores only public binding references. Wallet private keys and secret Lighter API keys are never accepted here. Commands stay pending until execution and reconciliation services return evidence. Withdrawals require an owner signature.</small>
      {(readiness.error || execution.error || mainnetDeposit.error || lighter.error || lighterConfirm.error || robinhood.error || robinhoodDeploy.error || lifecycle.error || activeLifecycleCommand.error || commandStatus.error || ownerAction.error) && <ErrorNotice error={readiness.error ?? execution.error ?? mainnetDeposit.error ?? lighter.error ?? lighterConfirm.error ?? robinhood.error ?? robinhoodDeploy.error ?? lifecycle.error ?? activeLifecycleCommand.error ?? commandStatus.error ?? ownerAction.error} />}
    </section>
  );
}

function matchesTerminal(status: AgentStatus) {
  return status === "setup" || status === "reducing" || status === "closing" || status === "closed";
}

function matchesProvisioning(status: AgentStatus) {
  return status === "provisioning" || status === "awaiting_signatures" || status === "awaiting_funding";
}

function canReadLiveExecution(status: AgentStatus) {
  return status === "ready"
    || status === "running"
    || status === "reducing"
    || status === "paused"
    || status === "closing"
    || status === "closed";
}

function terminalCommand(status?: AgentCommandRecord["status"]) {
  return status === "completed" || status === "rejected" || status === "failed";
}

function matchesLifecyclePanelCommand(command: AgentCommandRecord["command"]) {
  return command === "close" || command === "withdraw";
}

function formatReadinessBlocker(blocker: string) {
  return blocker.replaceAll("_", " ");
}

function executionStateLabel(state: string) {
  return state.replaceAll("_", " ");
}

function ExecutionTelemetry({ execution }: { execution: AgentExecutionStatus }) {
  const status = executionBadge(execution);
  return <div className="execution-telemetry" aria-live="polite">
    <div className="panel-heading">
      <div><span className="eyebrow">Coordinator episode</span><h3>Live execution</h3></div>
      <span className={`status-pill ${status.live ? "running" : "halted"}`}>{status.label}</span>
    </div>
    <dl className="detail-list large">
      <div><dt>Phase</dt><dd>{executionStateLabel(execution.state)}</dd></div>
      <div><dt>AAPL spot</dt><dd>{formatExecutionAmount(execution.spotAmountRaw, execution.spotDecimals, "AAPL")}</dd></div>
      <div><dt>AAPL perpetual</dt><dd>{formatExecutionAmount(execution.perpOpenBase, execution.perpBaseDecimals, "AAPL-PERP")}</dd></div>
      <div><dt>Approved gross</dt><dd>{formatGrossNotional(execution.spotNotionalMicros, execution.perpNotionalMicros)}</dd></div>
      <div><dt>Control</dt><dd>{execution.controlMode}</dd></div>
      <div><dt>Updated</dt><dd>{execution.updatedAtMs ? new Date(execution.updatedAtMs).toLocaleTimeString() : "Waiting"}</dd></div>
    </dl>
    {execution.intentId ? <small>Intent {formatAddress(execution.intentId)} · {execution.symbol}</small> : <small>Waiting for the first approved execution episode.</small>}
    {execution.lighterOrderId && <small>Lighter entry order {formatAddress(execution.lighterOrderId)}{execution.lighterTransactionHash ? ` · transaction ${formatAddress(execution.lighterTransactionHash)}` : ""}</small>}
    {execution.robinhoodTransactionHash && <small><a href={`${robinhoodMainnetExplorer}/tx/${execution.robinhoodTransactionHash}`} target="_blank" rel="noreferrer">Robinhood entry transaction {formatAddress(execution.robinhoodTransactionHash)} ↗</a></small>}
    {execution.lighterUnwindOrderId && <small>Lighter unwind order {formatAddress(execution.lighterUnwindOrderId)}{execution.lighterUnwindTransactionHash ? ` · transaction ${formatAddress(execution.lighterUnwindTransactionHash)}` : ""}</small>}
    {execution.robinhoodUnwindTransactionHash && <small><a href={`${robinhoodMainnetExplorer}/tx/${execution.robinhoodUnwindTransactionHash}`} target="_blank" rel="noreferrer">Robinhood unwind transaction {formatAddress(execution.robinhoodUnwindTransactionHash)} ↗</a></small>}
  </div>;
}

function executionBadge(execution: AgentExecutionStatus) {
  if (execution.flat) return { live: false, label: "Flat" };
  if (execution.accountStatus === "blocked" || execution.controlMode === "HALTED" || matchesExecutionFailure(execution.state)) {
    return { live: false, label: "Reconciling" };
  }
  if (execution.controlMode === "REDUCE_ONLY" || execution.state === "exiting" || execution.state === "unwinding") {
    return { live: false, label: "Reducing" };
  }
  if (execution.state === "hedged" && execution.active) return { live: true, label: "Live" };
  if (execution.active) return { live: false, label: "Entering" };
  return { live: false, label: "Reconciling" };
}

function readinessBadge(status: AgentStatus, canLaunch: boolean) {
  if (status === "closed") return { ready: false, label: "Closed" };
  if (status === "closing") return { ready: false, label: "Closing" };
  if (status === "reducing") return { ready: false, label: "Reducing" };
  if (status === "paused") return { ready: false, label: "Paused" };
  if (status === "blocked") return { ready: false, label: "Blocked" };
  if (status === "running") return { ready: true, label: "Running" };
  return canLaunch ? { ready: true, label: "Ready" } : { ready: false, label: "Blocked" };
}

function matchesExecutionFailure(state: string) {
  return state === "unhedged" || state === "failed_safe";
}

function formatExecutionAmount(raw: string, decimals: number, symbol: string) {
  return formatAmount({ raw, decimals, symbol }, 4);
}

function formatGrossNotional(spot: string, perp: string) {
  try {
    return formatAmount({ raw: (BigInt(spot) + BigInt(perp)).toString(), decimals: 6, symbol: "USD" });
  } catch {
    return "—";
  }
}

function requireAddress(value: string): `0x${string}` {
  if (!/^0x[0-9a-fA-F]{40}$/.test(value)) throw new Error("The verified account address is invalid.");
  return value as `0x${string}`;
}

function deploymentStorageKey(requestId: string) {
  return `robin:mainnet-deployment:${requestId}`;
}

function ownerActionStorageKey(commandId: string) {
  return `robin:owner-actions:${commandId}`;
}

function completedOwnerStorageKey(agentId: string) {
  return `robin:owner-transactions:${agentId}`;
}

function readTransactionHashes(key: string) {
  try {
    const value = JSON.parse(window.localStorage.getItem(key) ?? "[]");
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string" && /^0x[0-9a-fA-F]{64}$/.test(item)) : [];
  } catch {
    return [];
  }
}

function storeTransactionHashes(key: string, hashes: string[]) {
  try {
    window.localStorage.setItem(key, JSON.stringify(hashes));
  } catch {
    return;
  }
}

function removeStoredTransactions(key: string) {
  try {
    window.localStorage.removeItem(key);
  } catch {
    return;
  }
}

export function MandateButton({ dashboard }: { dashboard: DashboardSnapshot }) {
  const smartWallet = useSmartWallet();
  const queryClient = useQueryClient();
  const vault = dashboard.vault;
  const mutation = useMutation({
    mutationFn: async () => {
      if (!vault) throw new Error("Strategy controls are not configured.");
      return smartWallet.executeCalls([mandateCall(vault.record.guardAddress, !vault.halted)]);
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
  });

  if (!vault) return null;
  return (
    <div className="inline-action">
      <button className="button button-primary" disabled={mutation.isPending || smartWallet.pending} onClick={() => mutation.mutate()}>
        {mutation.isPending ? "Submitting…" : vault.halted ? "Start strategy" : "Pause strategy"}
      </button>
      {mutation.error && <span className="field-error" role="alert">{mutation.error.message}</span>}
    </div>
  );
}

export function WithdrawForm({ dashboard }: { dashboard: DashboardSnapshot }) {
  const smartWallet = useSmartWallet();
  const auth = useRobinAuth();
  const queryClient = useQueryClient();
  const [amount, setAmount] = useState("");
  const vault = dashboard.vault;
  const mutation = useMutation({
    mutationFn: async () => {
      if (!vault || !auth.embeddedAddress) throw new Error("Withdrawal is not available.");
      const raw = parseTokenAmount(amount, vault.balance.decimals);
      if (raw > BigInt(vault.balance.raw)) throw new Error("Amount exceeds the vault balance.");
      return smartWallet.executeCalls([
        withdrawalCall(vault.record.vaultAddress, auth.embeddedAddress, raw),
      ]);
    },
    onSuccess: () => {
      setAmount("");
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });

  if (!vault) return null;
  return (
    <form className="action-form" id="withdraw" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}>
      <label htmlFor="withdraw-amount">Amount to withdraw</label>
      <div className="amount-input"><input id="withdraw-amount" inputMode="decimal" value={amount} onChange={(event) => setAmount(event.target.value)} placeholder="0.00" /><span>{vault.balance.symbol}</span></div>
      <small>Destination: {formatAddress(auth.embeddedAddress)}</small>
      <button className="button button-secondary" disabled={mutation.isPending || !amount} type="submit">{mutation.isPending ? "Withdrawing…" : "Withdraw"}</button>
      {mutation.error && <span className="field-error" role="alert">{mutation.error.message}</span>}
    </form>
  );
}

export function AddFundsForm({ dashboard }: { dashboard: DashboardSnapshot }) {
  const api = useAppApi();
  const auth = useRobinAuth();
  const smartWallet = useSmartWallet();
  const queryClient = useQueryClient();
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: () => api.me() });
  const vault = dashboard.vault;
  const linkedAddresses = new Set(auth.accounts.map((wallet) => wallet.address.toLowerCase()));
  const eligible = me?.wallets.filter((wallet) => linkedAddresses.has(wallet.address.toLowerCase())) ?? [];
  const [wallet, setWallet] = useState("");
  const [amount, setAmount] = useState("");

  useEffect(() => {
    if (!wallet && eligible.length) setWallet(me?.preferences.activeFundingWallet ?? eligible[0].address);
  }, [eligible, me?.preferences.activeFundingWallet, wallet]);

  const mutation = useMutation({
    mutationFn: async () => {
      if (!vault || !wallet) throw new Error("Choose a connected funding wallet.");
      const raw = parseTokenAmount(amount, vault.balance.decimals);
      return smartWallet.executeCalls(
        depositCalls(vault.record.assetAddress, vault.record.vaultAddress, raw),
        wallet,
      );
    },
    onSuccess: () => {
      setAmount("");
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });

  if (!vault) return null;
  return (
    <form className="action-form" id="fund" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}>
      <label htmlFor="funding-wallet">Funding wallet</label>
      <select id="funding-wallet" value={wallet} onChange={(event) => setWallet(event.target.value)}>
        {!eligible.length && <option value="">No connected wallet</option>}
        {eligible.map((item) => <option value={item.address} key={item.address}>{item.label ?? item.walletType} · {formatAddress(item.address)}</option>)}
      </select>
      <label htmlFor="deposit-amount">Amount to add</label>
      <div className="amount-input"><input id="deposit-amount" inputMode="decimal" value={amount} onChange={(event) => setAmount(event.target.value)} placeholder="0.00" /><span>{vault.balance.symbol}</span></div>
      <small>Approval and deposit are submitted as one smart-account batch.</small>
      <button className="button button-primary" disabled={mutation.isPending || !amount || !wallet} type="submit">{mutation.isPending ? "Adding funds…" : "Add funds"}</button>
      {mutation.error && <ErrorNotice error={mutation.error} />}
    </form>
  );
}
