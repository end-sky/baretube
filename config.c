#include "config.h"
#include <string.h>

static void copy_value(char *dst, gsize cap, const char *src, const char *fallback) {
    const char *value = (src && *src) ? src : fallback;
    g_strlcpy(dst, value, cap);
}

void config_load(AppConfig *config) {
    GKeyFile *key = g_key_file_new();
    GError *error = NULL;
    gchar *path = g_build_filename(g_get_user_config_dir(), "baretube", "config.ini", NULL);
    gchar *backend = NULL, *instance = NULL, *theme = NULL;

    copy_value(config->backend, sizeof(config->backend), "local", "local");
    copy_value(config->instance, sizeof(config->instance), "https://yewtu.be", "https://yewtu.be");
    copy_value(config->theme, sizeof(config->theme), "amoled", "amoled");

    if (g_key_file_load_from_file(key, path, G_KEY_FILE_NONE, &error)) {
        backend = g_key_file_get_string(key, "General", "backend", NULL);
        instance = g_key_file_get_string(key, "General", "instance", NULL);
        theme = g_key_file_get_string(key, "Appearance", "theme", NULL);
        copy_value(config->backend, sizeof(config->backend), backend, "local");
        copy_value(config->instance, sizeof(config->instance), instance, "https://yewtu.be");
        copy_value(config->theme, sizeof(config->theme), theme, "amoled");
    }

    if (g_strcmp0(config->backend, "invidious") != 0)
        g_strlcpy(config->backend, "local", sizeof(config->backend));
    if (g_strcmp0(config->theme, "white") != 0)
        g_strlcpy(config->theme, "amoled", sizeof(config->theme));
    if (!g_str_has_prefix(config->instance, "https://") && !g_str_has_prefix(config->instance, "http://"))
        g_strlcpy(config->instance, "https://yewtu.be", sizeof(config->instance));

    g_free(backend);
    g_free(instance);
    g_free(theme);
    g_clear_error(&error);
    g_free(path);
    g_key_file_unref(key);
}

void config_save(const AppConfig *config) {
    GKeyFile *key = g_key_file_new();
    gchar *dir = g_build_filename(g_get_user_config_dir(), "baretube", NULL);
    gchar *path = g_build_filename(dir, "config.ini", NULL);
    gchar *contents;
    gsize length = 0;
    GError *error = NULL;

    g_key_file_set_string(key, "General", "backend", config->backend);
    g_key_file_set_string(key, "General", "instance", config->instance);
    g_key_file_set_string(key, "Appearance", "theme", config->theme);
    contents = g_key_file_to_data(key, &length, NULL);
    if (g_mkdir_with_parents(dir, 0700) == 0) {
        if (!g_file_set_contents(path, contents, (gssize)length, &error)) {
            g_warning("Could not save settings: %s", error ? error->message : "unknown error");
        }
    }

    g_clear_error(&error);
    g_free(contents);
    g_free(dir);
    g_free(path);
    g_key_file_unref(key);
}

void config_apply_theme(GtkWidget *widget, const char *theme) {
    static GtkCssProvider *provider = NULL;
    GdkScreen *screen;
    GError *error = NULL;
    const char *css_dark =
        "window, dialog { background: #000; color: #f5f5f5; }"
        "label { color: #f5f5f5; }"
        "entry, textview, spinbutton, combobox, button { color: #f5f5f5; background: #090909; border: 1px solid #454545; }"
        "button { padding: 6px 10px; }"
        "button:hover { background: #1a1a1a; }"
        "scrolledwindow, list, row { background: #000; color: #f5f5f5; }"
        "row { border-bottom: 1px solid #242424; }"
        ".dim-label { color: #a6a6a6; }";
    const char *css_white =
        "window, dialog { background: #fff; color: #111; }"
        "label { color: #111; }"
        "entry, textview, spinbutton, combobox, button { color: #111; background: #fff; border: 1px solid #ccc; }"
        "button { padding: 6px 10px; }"
        "button:hover { background: #f1f1f1; }"
        "scrolledwindow, list, row { background: #fff; color: #111; }"
        "row { border-bottom: 1px solid #e5e5e5; }"
        ".dim-label { color: #626262; }";

    if (!widget) return;
    screen = gtk_widget_get_screen(widget);
    if (!provider) provider = gtk_css_provider_new();
    gtk_css_provider_load_from_data(provider,
        g_strcmp0(theme, "white") == 0 ? css_white : css_dark, -1, &error);
    if (error) { g_warning("Could not load theme CSS: %s", error->message); g_clear_error(&error); }
    gtk_style_context_add_provider_for_screen(screen, GTK_STYLE_PROVIDER(provider), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
}
