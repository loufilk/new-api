# Data Table Components

This package keeps a stable public API through `index.ts`; feature code should
continue importing from `@/components/data-table`.

- `core/`: TanStack table rendering primitives, headers, rows, pagination,
  loading, empty states, and pinned-column behavior.
- `layout/`: responsive page-level composition that combines toolbar, desktop
  table, mobile list, bulk actions, and pagination placement.
- `toolbar/`: filter/search/view-option controls and selection action toolbar.
- `static/`: lightweight table rendering for local/static arrays that do not
  need TanStack state.
- `hooks/`: table state and filter hooks.

Keep feature-specific columns, actions, and dialogs inside their feature
folders. Shared table code belongs here only when it is reusable across more
than one feature.

`@/lib/console-table` binds the console's row, column, and table types to its
explicit TanStack Table v9 features. It uses the native React `useTable`, not
the deprecated legacy adapter. Use `useDataTable` for page state, persistence,
and client/server row processing. Choose the pipeline per table at mount:
server pages do not re-sort, filter, or paginate a returned page locally.
Read the subscribed `table.state` in views. Column and row objects themselves
can stay stable while selection, expansion, visibility or filter state changes;
a memoized child must receive those changes or subscribe to them.
