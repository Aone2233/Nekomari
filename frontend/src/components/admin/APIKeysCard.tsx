import { useCallback, useEffect, useState } from "react";
import { Button, Checkbox, Dialog, Flex, Text, TextField } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { SettingCard } from "@/components/admin/SettingCard";
import { useAccount } from "@/contexts/AccountContext";
import { formatTime } from "@/components/status/slaFormat";
import { toast } from "sonner";
import { requireApiSuccess } from "@/lib/apiResponse";

type APIKey = {
  id: string;
  name: string;
  scopes: string[];
  created_at: string;
  expires_at: string;
  revoked_at: string | null;
};

type KeyList = { keys: APIKey[]; scopes: string[] };
type KeyIssued = { key: APIKey; token: string };

async function keyRequest<T>(path = "", body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(`/api/admin/api-keys${path}`, {
    method: body === undefined ? "GET" : "POST",
    cache: "no-store",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  const payload = await requireApiSuccess<T>(response);
  return payload.data as T;
}

export default function APIKeysCard() {
  const { t, i18n } = useTranslation();
  const { account, loading: accountLoading, error: accountError } = useAccount();
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [scopes, setScopes] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [days, setDays] = useState("90");
  const [selected, setSelected] = useState<string[]>(["nodes:read"]);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<{ kind: "rotate" | "revoke"; key: APIKey } | null>(null);
  const [issued, setIssued] = useState<KeyIssued | null>(null);
  const [now, setNow] = useState(() => Date.now());

  const load = useCallback((signal?: AbortSignal) =>
    keyRequest<KeyList>("", undefined, signal)
      .then((data) => {
        if (signal?.aborted) return;
        setKeys(data.keys);
        setScopes(data.scopes);
        setError("");
      })
      .catch((error: unknown) => {
        if (signal?.aborted) return;
        setError(error instanceof Error ? error.message : String(error));
      })
      .finally(() => { if (!signal?.aborted) setLoading(false); }), []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  const needsCode = Boolean(account?.["2fa_enabled"]);
  const cannotMutate = busy || loading || accountLoading || Boolean(accountError) || !account?.logged_in || (needsCode && !code.trim());

  const mutate = async (kind: "create" | "rotate" | "revoke", key?: APIKey) => {
    if (cannotMutate) return;
    setBusy(true);
    setError("");
    try {
      const suffix = kind === "create" ? "" : `/${key!.id}/${kind}`;
      const result = await keyRequest<KeyIssued | null>(suffix, {
        ...(kind === "create" ? { name: name.trim(), scopes: selected, expires_days: Number(days) } : {}),
        ...(needsCode ? { "2fa_code": code.trim() } : {}),
      });
      if (kind !== "revoke" && result) setIssued(result);
      if (kind === "create") setName("");
      setPending(null);
      await load();
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      // A TOTP is not a reusable page-level credential.
      setCode("");
      setBusy(false);
    }
  };

  const otpInput = (id: string) => needsCode ? (
    <label htmlFor={id} className="flex flex-col gap-1">
      <Text size="2">{t("account.2fa_otp_input_prompt")}</Text>
      <TextField.Root id={id} value={code} onChange={(event) => setCode(event.target.value)}
        inputMode="numeric" autoComplete="one-time-code" maxLength={8} />
    </label>
  ) : null;

  // Error/list changes must not anchor the scroller to a different key under the pointer.
  return (
    <SettingCard title={t("api_keys.title")} description={t("api_keys.description")}
      className="[overflow-anchor:none]">
      <div className="w-full flex flex-col gap-4 py-3 min-w-0">
        <Text size="2" color="amber">{t("api_keys.migration")}</Text>
        {error && <Text role="alert" color="red">{error}</Text>}
        {accountError && <Text role="alert" color="red">{accountError.message}</Text>}
        <form className="flex flex-col gap-3" onSubmit={(event) => { event.preventDefault(); void mutate("create"); }}>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <label htmlFor="api-key-name" className="flex flex-col gap-1">
              <Text size="2">{t("api_keys.name")}</Text>
              <TextField.Root id="api-key-name" value={name} maxLength={80} required
                onChange={(event) => setName(event.target.value)} autoComplete="off" disabled={busy} />
            </label>
            <label htmlFor="api-key-days" className="flex flex-col gap-1">
              <Text size="2">{t("api_keys.expires_days")}</Text>
              <TextField.Root id="api-key-days" type="number" min={1} max={365} step={1} required
                value={days} onChange={(event) => setDays(event.target.value)} disabled={busy} />
            </label>
          </div>
          <fieldset disabled={busy} className="border rounded-md p-3 flex flex-col gap-2">
            <legend>{t("api_keys.scopes")}</legend>
            {scopes.map((scope) => (
              <Text as="label" size="2" key={scope} className="flex gap-2 items-start">
                <Checkbox aria-label={scope} checked={selected.includes(scope)} onCheckedChange={(checked) => {
                  setSelected((previous) => checked === true ? [...previous, scope] : previous.filter((item) => item !== scope));
                }} />
                <span><code>{scope}</code> - {t(`api_keys.scope_${scope.replaceAll(":", "_")}`)}</span>
              </Text>
            ))}
          </fieldset>
          <Text size="2" color="amber">{t("api_keys.danger")}</Text>
          {otpInput("api-key-otp")}
          <div><Button type="submit" disabled={cannotMutate || !name.trim() || selected.length === 0}>
            {t("api_keys.create")}
          </Button></div>
        </form>
        <Flex gap="2" align="center">
          <Text weight="bold">{t("api_keys.existing")}</Text>
          <Button variant="soft" disabled={busy} onClick={() => { void load(); }}>{t("common.refresh")}</Button>
        </Flex>
        {loading ? <Text>{t("common.loading")}</Text> : keys.length === 0 ? <Text>{t("api_keys.empty")}</Text> : keys.map((key) => {
          const revoked = Boolean(key.revoked_at);
          const expired = Date.parse(key.expires_at) <= now;
          return (
            <section key={key.id} aria-label={key.name} className="border rounded-md p-3 flex flex-col gap-2 min-w-0">
              <Text weight="bold" className="break-all">{key.name}</Text>
              <Text size="1" color="gray" className="break-all">{key.id}</Text>
              <Text size="2" className="break-words">{key.scopes.join(", ")}</Text>
              <Text size="2">{t("api_keys.expires")}: {formatTime(key.expires_at, i18n.language.replace("_", "-"))}</Text>
              <Text size="2">{t(revoked ? "api_keys.revoked" : expired ? "api_keys.expired" : "api_keys.active")}</Text>
              {!revoked && <Flex gap="2" wrap="wrap">
                {!expired && <Button type="button" variant="soft" disabled={busy || accountLoading || Boolean(accountError) || !account?.logged_in}
                  onClick={() => { setCode(""); setPending({ kind: "rotate", key }); }}>{t("api_keys.rotate")}</Button>}
                <Button type="button" variant="soft" color="red" disabled={busy || accountLoading}
                  onClick={() => { setCode(""); setPending({ kind: "revoke", key }); }}>{t("api_keys.revoke")}</Button>
              </Flex>}
            </section>
          );
        })}
      </div>
      <Dialog.Root open={pending !== null} onOpenChange={(open) => { if (!open && !busy) { setPending(null); setCode(""); } }}>
        <Dialog.Content maxWidth="500px">
          <Dialog.Title>{t(pending?.kind === "rotate" ? "api_keys.rotate" : "api_keys.revoke")}</Dialog.Title>
          <Dialog.Description>{t(pending?.kind === "rotate" ? "api_keys.rotate_warning" : "api_keys.revoke_warning", { name: pending?.key.name })}</Dialog.Description>
          <div className="mt-3 flex flex-col gap-3">
            {error && <Text role="alert" color="red">{error}</Text>}
            {otpInput("api-key-confirm-otp")}
            <Flex gap="3" justify="end">
              <Dialog.Close><Button variant="soft" disabled={busy}>{t("common.cancel")}</Button></Dialog.Close>
              <Button color="red" disabled={cannotMutate} onClick={() => { if (pending) void mutate(pending.kind, pending.key); }}>{t("common.confirm")}</Button>
            </Flex>
          </div>
        </Dialog.Content>
      </Dialog.Root>
      <Dialog.Root open={issued !== null} onOpenChange={(open) => { if (!open) setIssued(null); }}>
        <Dialog.Content maxWidth="600px">
          <Dialog.Title>{t("api_keys.secret_title")}</Dialog.Title>
          <Dialog.Description>{t("api_keys.secret_warning")}</Dialog.Description>
          <label htmlFor="api-key-secret" className="flex flex-col gap-2 my-4">
            <Text size="2">{t("api_keys.token")}</Text>
            <TextField.Root id="api-key-secret" readOnly value={issued?.token ?? ""} autoComplete="off" spellCheck={false} />
          </label>
          <Flex gap="3" justify="end" wrap="wrap">
            <Button variant="soft" onClick={async () => {
              try { await navigator.clipboard.writeText(issued?.token ?? ""); toast.success(t("common.copy_success")); }
              catch { toast.error(t("api_keys.copy_failed")); }
            }}>{t("common.copy")}</Button>
            <Dialog.Close><Button>{t("api_keys.saved")}</Button></Dialog.Close>
          </Flex>
        </Dialog.Content>
      </Dialog.Root>
    </SettingCard>
  );
}
