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
 *
 * Every column other than the title is pinned, which is what makes the title
 * the one that takes the rest of the row when the widths are fitted. A ⋯ is
 * added where the list had none: Safari on an iPhone has no right click, so it
 * is the reliable way to the queue and playlist actions.
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

    const out: ItemTableListColumnConfig[] = [];
    const combined = title.id === TableColumn.TITLE_COMBINED;
    if (!combined) {
        // An album's tracks are numbered and share one cover: keep the number.
        const lead = find(TableColumn.TRACK_NUMBER) ?? find(TableColumn.ROW_INDEX);
        const image = find(TableColumn.IMAGE);
        if (image) out.push({ ...image, autoSize: false, pinned: 'left', width: 56 });
        else if (lead) out.push({ ...lead, align: 'center', autoSize: false, pinned: 'left', width: 36 });
    }
    out.push({ ...title, autoSize: false, pinned: null, width: 240 });
    const duration = find(TableColumn.DURATION);
    if (duration) out.push({ ...duration, align: 'end', autoSize: false, pinned: 'right', width: 52 });
    const actions = find(TableColumn.ACTIONS) ?? {
        align: 'center',
        id: TableColumn.ACTIONS,
        isEnabled: true,
        pinned: 'right',
        width: 44,
    };
    out.push({ ...actions, align: 'center', autoSize: false, pinned: 'right', width: 44 });
    return out;
};

export const useSamoPhoneColumns = (columns: ItemTableListColumnConfig[]) => {
    const isMobile = useIsMobile();
    return useMemo(() => (isMobile ? phoneColumns(columns) : null), [columns, isMobile]);
};
