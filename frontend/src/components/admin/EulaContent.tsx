import { Button } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import { getEula } from "@/utils/eula";

export default function EulaContent({ onAccept }: { onAccept: () => Promise<void> }) {
  const { t, i18n } = useTranslation();
  return (
    <div className="km-admin-eula-content flex flex-col gap-2">
      <div className="max-h-[70vh] overflow-y-auto space-y-4">
        <pre className="text-wrap">{getEula(i18n.language)}</pre>
      </div>
      <div className="flex flex-row gap-2 justify-end items-center">
        <Button variant="soft" color="red" onClick={() => window.close()}>{t("eula.reject")}</Button>
        <Button variant="solid" onClick={onAccept}>{t("eula.accept")}</Button>
      </div>
    </div>
  );
}
