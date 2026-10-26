import { Button, Flex, Text } from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import React from "react";
import { Link } from "react-router-dom";
import { bulkEditHref } from "./bulkSelection";

/**
 * The bridge from the node list's checkboxes to the existing bulk edit page.
 *
 * Use the same in-panel route as the sidebar, carrying the selection in the
 * query string so reloads and copied links keep it without a second document.
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
        <Link
          to={bulkEditHref(selectedNodes)}
          data-testid="bulk-entry-link"
          data-selected-count={String(selectedNodes.length)}
        >
          {t("bulk.title")}
        </Link>
      </Button>
      <Text size="1" color="gray" data-testid="bulk-entry-count">
        {t("bulk.selectionCount").replace("{{count}}", String(selectedNodes.length))}
      </Text>
    </Flex>
  );
};

export default BulkEntry;
