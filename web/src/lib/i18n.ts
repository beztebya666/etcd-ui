// Minimal gettext-style i18n. English keys are written literally in JSX as
// t('Dashboard'); a dictionary supplies translations. Unknown keys fall back
// to the English text, so missing translations degrade gracefully.

import { useStore } from "./store";

export type Lang = "en" | "ru";

const ru: Record<string, string> = {
  // Nav
  "Dashboard": "Панель",
  "Keys": "Ключи",
  "Watch": "Наблюдение",
  "Transactions": "Транзакции",
  "Cluster": "Кластер",
  "Users & Roles": "Пользователи и роли",
  "Audit": "Аудит",
  "Maintenance": "Обслуживание",
  "Settings": "Настройки",
  "Metrics": "Метрики",
  "Diff": "Сравнение",

  // Common
  "Save": "Сохранить",
  "Cancel": "Отмена",
  "Delete": "Удалить",
  "Confirm": "Подтвердить",
  "Refresh": "Обновить",
  "Add": "Добавить",
  "Search": "Поиск",
  "Loading…": "Загрузка…",
  "No matches.": "Ничего не найдено.",
  "Are you sure?": "Вы уверены?",
  "This action cannot be undone.": "Это действие нельзя отменить.",
  "OK": "ОК",

  // Topbar / Picker
  "Search keys, jump to cluster, run commands…": "Поиск ключей, переход к кластеру, команды…",
  "no clusters": "нет кластеров",
  "loading…": "загрузка…",
  "cluster": "кластер",

  // Dashboard
  "Fleet overview": "Обзор парка",
  "Every etcd cluster you connect to — Kubernetes control-plane, Patroni DCS, standalone — shown in one place, updated live.":
    "Каждый кластер etcd — control-plane Kubernetes, Patroni DCS, standalone — в одном месте, в реальном времени.",
  "No clusters detected yet": "Кластеры не обнаружены",
  "members": "узлы",
  "leader": "лидер",
  "db size": "размер БД",
  "revision": "ревизия",

  // Settings
  "Theme": "Тема",
  "Dark": "Тёмная",
  "Light": "Светлая",
  "System": "Системная",
  "Language": "Язык",
  "Simple mode": "Простой режим",
  "Friendlier labels, advanced features hidden. Recommended for newcomers.":
    "Понятные подписи, продвинутые функции скрыты. Рекомендуется новичкам.",
  "Replay onboarding tour": "Показать обучение",
  "Walk through the app in 30 seconds.": "Знакомство с приложением за 30 секунд.",
  "Clusters": "Кластеры",
  "Add cluster…": "Добавить кластер…",

  // Toasts (default messages)
  "Saved": "Сохранено",
  "Deleted": "Удалено",
  "Copied": "Скопировано",
  "Failed": "Ошибка",

  // RBAC
  "auth enabled": "авторизация включена",
  "auth disabled": "авторизация выключена",
  "Enable auth": "Включить авторизацию",
  "Disable": "Выключить",
  "Users": "Пользователи",
  "Roles": "Роли",
  "Name": "Имя",
  "Grant": "Выдать",
  "Add user": "Добавить пользователя",
  "Add role": "Добавить роль",
  "Grant permission": "Выдать права",
  "username": "имя пользователя",
  "password": "пароль",
  "no permissions": "нет прав",
  "prefix": "по префиксу",
  "read": "чтение",
  "write": "запись",
  "readwrite": "чтение/запись",

  // Txn
  "If": "Если",
  "Then": "Тогда",
  "Else": "Иначе",
  "all of these must be true": "все условия истинны",
  "run these on success": "выполнить при успехе",
  "run these on failure": "выполнить при неудаче",
  "Commit transaction": "Выполнить транзакцию",
  "Committing…": "Выполняется…",
  "Add condition": "Добавить условие",
  "succeeded · rev": "успех · ревизия",
  "else branch ran · rev": "ветка else · ревизия",

  // Diff
  "Cluster diff": "Сравнение кластеров",
  "Pairwise compare two etcd clusters under a prefix. Identical keys are hidden.":
    "Попарное сравнение кластеров под префиксом. Совпадающие ключи скрыты.",
  "Compare": "Сравнить",
  "Left (current)": "Левый (текущий)",
  "Right (compare to)": "Правый (с чем сравниваем)",
  "All": "Все",
  "Only left": "Только слева",
  "Only right": "Только справа",
  "Different": "Различаются",
  "All keys match. 🎉": "Все ключи совпадают. 🎉",

  // Metrics
  "Live from etcd's": "В реальном времени из etcd",
  "has leader": "лидер есть",
  "no leader": "нет лидера",
  "Refreshed every 2 s.": "Обновление каждые 2 сек.",
  "db in-use": "БД использовано",
  "rss": "память",
  "leader changes": "смены лидера",

  // Heatmap
  "Write heatmap": "Карта записей",
  "Where in the keyspace is your cluster actually being written? Aggregates live events per prefix bucket.":
    "Куда в keyspace реально пишут? Агрегация по префиксам в реальном времени.",
  "bucket depth": "глубина бакета",
  "Waiting for events…": "Ожидание событий…",
  "Reset": "Сброс",
  "Pause": "Пауза",
  "Resume": "Продолжить",

  // Restore recipe
  "Snapshot (.db) restore": "Восстановление снапшота (.db)",
  "Generate recipe": "Сгенерировать рецепт",
  "Generating…": "Генерация…",

  // Browser actions
  "History": "История",
  "Format JSON": "Формат JSON",
  "Pick a key on the left to edit, or hit": "Выберите ключ слева, чтобы редактировать, или нажмите",
  "to create one.": "чтобы создать новый.",
  "Select a cluster from the top-right picker to start browsing keys.":
    "Выберите кластер в правом верхнем углу, чтобы начать.",
  "search inside values (regex)…": "поиск по значениям (regex)…",

  // Audit
  "Audit log": "Аудит",
  "Every write performed via this UI is recorded here and persisted on disk.":
    "Каждая запись через UI фиксируется и сохраняется на диск.",
  "No events match.": "Нет событий по фильтру.",

  // Onboarding
  "Hello! 👋": "Привет! 👋",
  "Skip tour": "Пропустить",
  "Next": "Дальше",
  "Back": "Назад",
  "Got it!": "Понятно!",

  // Common controls
  "All ({total})": "Все ({total})",
  "Bulk import": "Массовый импорт",
  "Choose file…": "Выбрать файл…",
};

const dicts: Record<Lang, Record<string, string>> = { en: {}, ru };

export function t(key: string): string {
  const lang = useStore.getState().lang;
  return dicts[lang]?.[key] ?? key;
}

// Hook variant so components re-render when language changes.
export function useT() {
  const lang = useStore((s) => s.lang);
  return (key: string) => dicts[lang]?.[key] ?? key;
}
