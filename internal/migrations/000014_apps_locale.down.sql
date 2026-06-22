ALTER TABLE apps
    DROP CONSTRAINT apps_locale_check,
    DROP COLUMN locale;
