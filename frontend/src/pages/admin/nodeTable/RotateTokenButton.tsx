import { useRef, useState } from "react";
import { Button, Checkbox, Dialog, Flex, IconButton, Text, TextField } from "@radix-ui/themes";
import { KeyRound } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAccount } from "@/contexts/AccountContext";
import { useNodeDetails, type NodeDetail } from "@/contexts/NodeDetailsContext";
import { requireApiSuccess } from "@/lib/apiResponse";

export function RotateTokenButton({ node }: { node: NodeDetail }) {
  const { t } = useTranslation();
  const { account, loading, error: accountError } = useAccount();
  const { refresh } = useNodeDetails();
  const [open, setOpen] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  const [code, setCode] = useState("");
  const [issued, setIssued] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const needsCode = Boolean(account?.["2fa_enabled"]);
  const disabled = loading || Boolean(accountError) || !account?.logged_in;

  const changeOpen = (next: boolean) => {
    if (submitting.current) return;
    setOpen(next);
    setAcknowledged(false);
    setCode("");
    setIssued("");
    setError("");
  };

  const rotate = async () => {
    if (submitting.current || disabled || !acknowledged || (needsCode && !code.trim())) return;
    submitting.current = true;
    setBusy(true);
    setError("");
    try {
      const response = await fetch(`/api/admin/client/${encodeURIComponent(node.uuid)}/token/rotate`, {
        method: "POST", cache: "no-store", headers: { "Content-Type": "application/json" },
        body: JSON.stringify(needsCode ? { "2fa_code": code.trim() } : {}),
      });
      const payload = await requireApiSuccess(response);
      if (typeof payload.token !== "string" || !payload.token) throw new Error(t("client_token.uncertain"));
      setIssued(payload.token);
    } catch (cause) {
      // A lost response may follow a committed rotation; never replay this write.
      const detail = cause instanceof Error ? cause.message : "";
      const uncertain = t("client_token.uncertain");
      setError(detail && detail !== uncertain ? `${detail} ${uncertain}` : uncertain);
    } finally {
      refresh();
      setAcknowledged(false);
      setCode("");
      submitting.current = false;
      setBusy(false);
    }
  };

  return (
    <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Trigger>
        <IconButton variant="ghost" title={t("client_token.rotate")} aria-label={t("client_token.rotate")} disabled={disabled}>
          <KeyRound size="18" />
        </IconButton>
      </Dialog.Trigger>
      <Dialog.Content maxWidth="560px" onEscapeKeyDown={(event) => { if (busy) event.preventDefault(); }}
        onPointerDownOutside={(event) => { if (busy) event.preventDefault(); }}>
        <Dialog.Title>{t("client_token.rotate")}</Dialog.Title>
        <Dialog.Description>{t("client_token.warning", { name: node.name })}</Dialog.Description>
        <div className="my-4 flex flex-col gap-3 min-w-0">
          {error && <Text role="alert" color="red">{error}</Text>}
          {issued ? <>
            <Text size="2">{t("client_token.install")}</Text>
            <label className="flex flex-col gap-1">
              <Text size="2">{t("client_token.token")}</Text>
              <TextField.Root readOnly value={issued} autoComplete="off" spellCheck={false} />
            </label>
            <Flex gap="3" justify="end" wrap="wrap">
              <Button variant="soft" onClick={async () => {
                try { await navigator.clipboard.writeText(issued); }
                catch { setError(t("api_keys.copy_failed")); }
              }}>{t("common.copy")}</Button>
              <Dialog.Close><Button>{t("client_token.saved")}</Button></Dialog.Close>
            </Flex>
          </> : <form className="flex flex-col gap-3" onSubmit={(event) => { event.preventDefault(); void rotate(); }}>
            <Text as="label" size="2" className="flex items-start gap-2">
              <Checkbox checked={acknowledged} disabled={busy} onCheckedChange={(value) => setAcknowledged(value === true)} />
              {t("client_token.recovery")}
            </Text>
            {needsCode && <label className="flex flex-col gap-1">
              <Text size="2">{t("account.2fa_otp_input_prompt")}</Text>
              <TextField.Root value={code} onChange={(event) => setCode(event.target.value)}
                inputMode="numeric" autoComplete="one-time-code" maxLength={8} disabled={busy} />
            </label>}
            <Flex gap="3" justify="end" wrap="wrap">
              <Dialog.Close><Button type="button" variant="soft" disabled={busy}>{t("common.cancel")}</Button></Dialog.Close>
              <Button type="submit" color="red" disabled={disabled || busy || !acknowledged || (needsCode && !code.trim())}>
                {t("client_token.confirm")}
              </Button>
            </Flex>
          </form>}
        </div>
      </Dialog.Content>
    </Dialog.Root>
  );
}
