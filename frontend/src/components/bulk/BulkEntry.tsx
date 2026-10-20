import { Button, Flex, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React from "react";
import { bulkEditHref } from "./bulkSelection";

/**
 * The bridge from the node list's checkboxes to the existing bulk edit page.
 *
 * The bulk page is a separate document, so this is a link rather than a route change, and it
 * opens in a new tab: navigating the admin document away would throw away the list, the search
 * term and the scroll position that produced the selection. The menu entry for the same page is
 * a new-tab link for the same reason.
 *
 * It renders nothing without a selection. A "bulk edit" button that opens an empty page is a
 * button that teaches the operator the feature is broken, and the empty case is the one every
 * visitor sees first.
 */
export const BulkEntry: React.FC<{ selectedNodes: string[] }> = ({ selectedNodes }) => {
  const { t } = useTranslation();
  if (selectedNodes.length === 0) return null;

  return (
    <Flex align="center" gap="3" wrap="wrap" data-testid="bulk-entry">
      <Button asChild size="2">
        <a
          href={bulkEditHref(selectedNodes)}
          target="_blank"
          rel="noopener noreferrer"
          data-testid="bulk-entry-link"
          data-selected-count={String(selectedNodes.length)}
        >
          {t("bulk.title")}
        </a>
      </Button>
      <Text size="1" color="gray" data-testid="bulk-entry-count">
        {t("bulk.selectionCount").replace("{{count}}", String(selectedNodes.length))}
      </Text>
    </Flex>
  );
};

export default BulkEntry;
