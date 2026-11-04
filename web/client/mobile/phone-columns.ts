import { useMemo } from 'react';

import { ItemTableListColumnConfig } from '/@/renderer/components/item-list/types';
import { useIsMobile } from '/@/renderer/hooks/use-is-mobile';
import { TableColumn } from '/@/shared/types/types';

const TITLES = [TableColumn.TITLE_COMBINED, TableColumn.TITLE_ARTIST, TableColumn.TITLE];

/**
 * A phone has room for what the Android app shows in a row: the cover, the
 * title over the artist, the length, and a ⋯ for everything else. The desktop
 * table (album, genre, year, plays, heart...) squeezed into 390px left the
 * title one letter wide, so on a phone the table keeps only those columns.
 * A ⋯ is added where the list had none: Safari on an iPhone has no right
 * click, so it is the reliable way to the queue and playlist actions.
 *
 * Nothing is pinned. The table draws pinned columns as panes of their own and
 * keeps them level with JavaScript on every scroll event, which under a
 * finger's momentum made lists judder back and forth. Unpinned, the row is
 * one pane the browser scrolls natively. The widths are proportions of the
 * row (the table fits them to it), set so the title takes most of it.
 *
 * A table with no title column is some other shape, and is left alone.
 */
export const phoneColumns = (
    columns: ItemTableListColumnConfig[],
): ItemTableListColumnConfig[] | null => {
    const enabled = columns.filter((column) => column.isEnabled !== false);
    const find = (id: TableColumn) => enabled.find((column) => column.id === id);
    const title = TITLES.map(find).find(Boolean);
    if (!title) return null;

    const fixed = { autoSize: false, pinned: null } as const;
    const out: ItemTableListColumnConfig[] = [];
    if (title.id !== TableColumn.TITLE_COMBINED) {
        // An album's tracks are numbered and share one cover: keep the number.
        const lead = find(TableColumn.TRACK_NUMBER) ?? find(TableColumn.ROW_INDEX);
        const image = find(TableColumn.IMAGE);
        if (image) out.push({ ...image, ...fixed, width: 56 });
        else if (lead) out.push({ ...lead, ...fixed, align: 'center', width: 36 });
    }
    out.push({ ...title, ...fixed, width: 250 });
    const duration = find(TableColumn.DURATION);
    if (duration) out.push({ ...duration, ...fixed, align: 'end', width: 52 });
    const actions = find(TableColumn.ACTIONS) ?? {
        align: 'center',
        id: TableColumn.ACTIONS,
        isEnabled: true,
        pinned: null,
        width: 44,
    };
    out.push({ ...actions, ...fixed, align: 'center', width: 44 });
    return out;
};

export const useSamoPhoneColumns = (columns: ItemTableListColumnConfig[]) => {
    const isMobile = useIsMobile();
    return useMemo(() => (isMobile ? phoneColumns(columns) : null), [columns, isMobile]);
};
