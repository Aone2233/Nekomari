import { Button } from "@radix-ui/themes";
import { formatTime } from "@/components/status/slaFormat";
import type { GithubReleaseInfo } from "@/lib/releaseFeed";

export default function ReleaseNotesContent({ releases, currentVersion }: {
  releases: GithubReleaseInfo[];
  currentVersion?: string;
}) {
  const latest = releases[0];
  return (
    <div className="flex flex-col gap-2 max-w-[80vw] md:max-w-[720px]">
      <div className="text-sm text-muted-foreground">{currentVersion} {">"} {latest.tag_name || latest.name}</div>
      <div className="rounded-md p-2 overflow-auto max-h-80">
        <div className="flex flex-col gap-4 text-sm">
          {releases.map(release => (
            <div key={release.html_url} className="flex flex-col gap-2">
              <div className="flex items-center justify-between">
                <div className="font-medium">{release.name || release.tag_name}</div>
                {release.published_at && <div className="text-xs text-muted-foreground">{formatTime(release.published_at)}</div>}
              </div>
              <div className="whitespace-pre-wrap break-words">{release.body}</div>
              <div style={{ height: 1, background: "var(--accent-5)", opacity: 0.5 }} />
            </div>
          ))}
        </div>
      </div>
      <div className="flex justify-end">
        <Button asChild variant="soft"><a href={latest.html_url} target="_blank" rel="noopener noreferrer">GitHub</a></Button>
      </div>
    </div>
  );
}
