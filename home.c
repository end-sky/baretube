#include "home.h"
#include <string.h>

#define RESULT_LIMIT 30

typedef struct {
    AppState *app;
    char *query;
    char backend[24];
    char instance[256];
    guint generation;
} SearchJob;

typedef struct {
    AppState *app;
    char *video_id;
    char *mode;
    char backend[24];
    char instance[256];
} StreamJob;

typedef struct {
    char *output;
    char *error_text;
    gboolean ok;
} CommandResult;

static CommandResult *run_command(char **argv) {
    CommandResult *result = g_new0(CommandResult, 1);
    gint status = 0;
    GError *error = NULL;
    if (!g_spawn_sync(NULL, argv, NULL, G_SPAWN_SEARCH_PATH,
                      NULL, NULL, &result->output, &result->error_text,
                      &status, &error)) {
        result->error_text = g_strdup(error ? error->message : "could not start scraper process");
        g_clear_error(&error);
        result->ok = FALSE;
        return result;
    }
    result->ok = (status == 0);
    if (!result->ok && (!result->error_text || !*result->error_text)) {
        g_free(result->error_text);
        result->error_text = g_strdup("scraper exited with an error");
    }
    return result;
}

static void command_result_free(CommandResult *result) {
    if (!result) return;
    g_free(result->output);
    g_free(result->error_text);
    g_free(result);
}

static void clear_results(AppState *app) {
    GList *children = gtk_container_get_children(GTK_CONTAINER(app->results));
    for (GList *it = children; it; it = it->next) gtk_widget_destroy(GTK_WIDGET(it->data));
    g_list_free(children);
}

static void set_status(AppState *app, const char *text) {
    gtk_label_set_text(GTK_LABEL(app->status), text ? text : "");
}

static void search_job_free(gpointer data) {
    SearchJob *job = data;
    if (!job) return;
    g_free(job->query);
    g_free(job);
}

static void stream_job_free(gpointer data) {
    StreamJob *job = data;
    if (!job) return;
    g_free(job->video_id);
    g_free(job->mode);
    g_free(job);
}

static void search_worker(GTask *task, gpointer source_object, gpointer task_data, GCancellable *cancel) {
    SearchJob *job = task_data;
    AppState *app = job->app;
    char *argv[] = {app->backend_path, "search", job->backend, job->instance, job->query, NULL};
    CommandResult *result = run_command(argv);
    if (g_task_return_error_if_cancelled(task)) { command_result_free(result); return; }
    g_task_return_pointer(task, result, (GDestroyNotify)command_result_free);
    (void)source_object;
    (void)cancel;
}

static void show_error(AppState *app, const char *heading, const char *message) {
    GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(app->window), GTK_DIALOG_MODAL,
        GTK_MESSAGE_WARNING, GTK_BUTTONS_CLOSE, "%s", heading);
    gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog), "%s", message ? message : "Unknown error");
    gtk_dialog_run(GTK_DIALOG(dialog));
    gtk_widget_destroy(dialog);
}

static void launch_player(AppState *app, const char *mode, const char *output) {
    gchar **parts = g_strsplit(output ? output : "", "\t", 2);
    const char *video_url = parts[0] ? g_strstrip(parts[0]) : "";
    const char *audio_url = parts[1] ? g_strstrip(parts[1]) : "";
    GError *error = NULL;
    gboolean launched = FALSE;
    if (!*video_url) {
        show_error(app, "No playable stream", "The scraper did not return a playable URL. Try the other backend in Settings.");
    } else if (g_strcmp0(mode, "audio") == 0) {
        char *argv[] = {app->mpv_path, "--no-video", "--", (char *)video_url, NULL};
        launched = g_spawn_async(NULL, argv, NULL, G_SPAWN_SEARCH_PATH, NULL, NULL, NULL, &error);
    } else if (*audio_url) {
        char *audio_arg = g_strdup_printf("--audio-file=%s", audio_url);
        char *argv[] = {app->mpv_path, "--force-window=yes", audio_arg, "--", (char *)video_url, NULL};
        launched = g_spawn_async(NULL, argv, NULL, G_SPAWN_SEARCH_PATH, NULL, NULL, NULL, &error);
        g_free(audio_arg);
    } else {
        char *argv[] = {app->mpv_path, "--force-window=yes", "--", (char *)video_url, NULL};
        launched = g_spawn_async(NULL, argv, NULL, G_SPAWN_SEARCH_PATH, NULL, NULL, NULL, &error);
    }
    if (!launched && error) {
        show_error(app, "Could not launch mpv", error->message);
        g_clear_error(&error);
    }
    g_strfreev(parts);
}

static void stream_worker(GTask *task, gpointer source_object, gpointer task_data, GCancellable *cancel) {
    StreamJob *job = task_data;
    AppState *app = job->app;
    char *argv[] = {app->backend_path, "stream", job->backend, job->instance, job->video_id, job->mode, NULL};
    CommandResult *result = run_command(argv);
    if (g_task_return_error_if_cancelled(task)) { command_result_free(result); return; }
    g_task_return_pointer(task, result, (GDestroyNotify)command_result_free);
    (void)source_object;
    (void)cancel;
}

static void stream_done(GObject *source, GAsyncResult *async_result, gpointer user_data) {
    AppState *app = user_data;
    GError *error = NULL;
    CommandResult *result = g_task_propagate_pointer(G_TASK(async_result), &error);
    if (error) {
        show_error(app, "Stream lookup failed", error->message);
        g_clear_error(&error);
        return;
    }
    if (!result || !result->ok) {
        show_error(app, "Stream lookup failed", result ? result->error_text : "No response from scraper");
    } else {
        launch_player(app, "video", result->output);
    }
    command_result_free(result);
    (void)source;
}

static void stream_audio_done(GObject *source, GAsyncResult *async_result, gpointer user_data) {
    AppState *app = user_data;
    GError *error = NULL;
    CommandResult *result = g_task_propagate_pointer(G_TASK(async_result), &error);
    if (error) {
        show_error(app, "Audio lookup failed", error->message);
        g_clear_error(&error);
        return;
    }
    if (!result || !result->ok) {
        show_error(app, "Audio lookup failed", result ? result->error_text : "No response from scraper");
    } else {
        launch_player(app, "audio", result->output);
    }
    command_result_free(result);
    (void)source;
}

static void start_stream(AppState *app, const char *video_id, const char *mode) {
    StreamJob *job = g_new0(StreamJob, 1);
    job->app = app;
    job->video_id = g_strdup(video_id);
    job->mode = g_strdup(mode);
    g_strlcpy(job->backend, app->config.backend, sizeof(job->backend));
    g_strlcpy(job->instance, app->config.instance, sizeof(job->instance));
    GTask *task = g_task_new(app->window, NULL,
        g_strcmp0(mode, "audio") == 0 ? stream_audio_done : stream_done, app);
    g_task_set_task_data(task, job, stream_job_free);
    g_task_run_in_thread(task, stream_worker);
    g_object_unref(task);
    set_status(app, g_strcmp0(mode, "audio") == 0 ? "Finding audio stream…" : "Finding video stream…");
}

static void play_clicked(GtkButton *button, gpointer user_data) {
    GtkWidget *row = GTK_WIDGET(user_data);
    AppState *app = g_object_get_data(G_OBJECT(row), "app-state");
    const char *id = g_object_get_data(G_OBJECT(row), "video-id");
    if (app && id) start_stream(app, id, "video");
    (void)button;
}

static void audio_clicked(GtkButton *button, gpointer user_data) {
    GtkWidget *row = GTK_WIDGET(user_data);
    AppState *app = g_object_get_data(G_OBJECT(row), "app-state");
    const char *id = g_object_get_data(G_OBJECT(row), "video-id");
    if (app && id) start_stream(app, id, "audio");
    (void)button;
}

static GtkWidget *make_video_row(AppState *app, char **fields) {
    GtkWidget *row = gtk_list_box_row_new();
    GtkWidget *line = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 10);
    GtkWidget *stack = gtk_box_new(GTK_ORIENTATION_VERTICAL, 3);
    GtkWidget *title = gtk_label_new(fields[1] && *fields[1] ? fields[1] : "Untitled video");
    GtkWidget *meta;
    GtkWidget *buttons = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 6);
    char *meta_text = g_strdup_printf("%s%s%s",
        fields[2] && *fields[2] ? fields[2] : "YouTube",
        fields[3] && *fields[3] ? "  ·  " : "",
        fields[3] && *fields[3] ? fields[3] : "");
    meta = gtk_label_new(meta_text);
    g_free(meta_text);
    gtk_widget_set_halign(title, GTK_ALIGN_START);
    gtk_label_set_ellipsize(GTK_LABEL(title), PANGO_ELLIPSIZE_END);
    gtk_label_set_max_width_chars(GTK_LABEL(title), 64);
    gtk_widget_set_halign(meta, GTK_ALIGN_START);
    gtk_style_context_add_class(gtk_widget_get_style_context(meta), "dim-label");
    gtk_box_pack_start(GTK_BOX(stack), title, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(stack), meta, FALSE, FALSE, 0);
    gtk_widget_set_hexpand(stack, TRUE);
    gtk_box_pack_start(GTK_BOX(line), stack, TRUE, TRUE, 4);
    GtkWidget *play = gtk_button_new_with_label("Play video");
    GtkWidget *audio = gtk_button_new_with_label("Audio");
    gtk_box_pack_start(GTK_BOX(buttons), play, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(buttons), audio, FALSE, FALSE, 0);
    gtk_box_pack_end(GTK_BOX(line), buttons, FALSE, FALSE, 0);
    gtk_container_add(GTK_CONTAINER(row), line);
    g_object_set_data_full(G_OBJECT(row), "video-id", g_strdup(fields[0]), g_free);
    g_object_set_data(G_OBJECT(row), "app-state", app);
    g_signal_connect(play, "clicked", G_CALLBACK(play_clicked), row);
    g_signal_connect(audio, "clicked", G_CALLBACK(audio_clicked), row);
    gtk_widget_set_margin_start(row, 8);
    gtk_widget_set_margin_end(row, 8);
    gtk_widget_set_margin_top(row, 5);
    gtk_widget_set_margin_bottom(row, 5);
    return row;
}

static void search_done(GObject *source, GAsyncResult *async_result, gpointer user_data) {
    AppState *app = user_data;
    SearchJob *job = g_task_get_task_data(G_TASK(async_result));
    GError *error = NULL;
    CommandResult *result = g_task_propagate_pointer(G_TASK(async_result), &error);
    if (job && job->generation != app->generation) {
        if (result) command_result_free(result);
        g_clear_error(&error);
        return;
    }
    gtk_widget_set_sensitive(app->search_button, TRUE);
    clear_results(app);
    if (error || !result || !result->ok) {
        const char *msg = error ? error->message : (result ? result->error_text : "No response from scraper");
        set_status(app, "Search failed.");
        GtkWidget *label = gtk_label_new(msg ? msg : "Unknown error");
        gtk_label_set_line_wrap(GTK_LABEL(label), TRUE);
        gtk_widget_set_margin_start(label, 16);
        gtk_widget_set_margin_end(label, 16);
        gtk_widget_set_margin_top(label, 20);
        gtk_list_box_insert(GTK_LIST_BOX(app->results), label, -1);
    } else {
        guint count = 0;
        gchar **lines = g_strsplit(result->output ? result->output : "", "\n", -1);
        for (guint i = 0; lines[i] && count < RESULT_LIMIT; i++) {
            if (!*lines[i]) continue;
            gchar **fields = g_strsplit(lines[i], "\t", 4);
            if (fields[0] && *fields[0]) {
                GtkWidget *row = make_video_row(app, fields);
                gtk_list_box_insert(GTK_LIST_BOX(app->results), row, -1);
                count++;
            }
            g_strfreev(fields);
        }
        g_strfreev(lines);
        if (count == 0) {
            GtkWidget *label = gtk_label_new("No results. Try another query or backend.");
            gtk_widget_set_margin_top(label, 20);
            gtk_list_box_insert(GTK_LIST_BOX(app->results), label, -1);
        }
        gchar *summary = g_strdup_printf("%u results · no subscriptions, likes, or history are stored", count);
        set_status(app, summary);
        g_free(summary);
    }
    if (result) command_result_free(result);
    g_clear_error(&error);
    gtk_widget_show_all(app->results);
    (void)source;
}

void home_search(AppState *app) {
    const char *query = gtk_entry_get_text(GTK_ENTRY(app->search_entry));
    if (!query || !*query) { set_status(app, "Type something to search for."); return; }
    SearchJob *job = g_new0(SearchJob, 1);
    job->app = app;
    job->query = g_strdup(query);
    g_strlcpy(job->backend, app->config.backend, sizeof(job->backend));
    g_strlcpy(job->instance, app->config.instance, sizeof(job->instance));
    job->generation = ++app->generation;
    gtk_widget_set_sensitive(app->search_button, FALSE);
    set_status(app, "Searching…");
    clear_results(app);
    GtkWidget *label = gtk_label_new("Searching YouTube…");
    gtk_widget_set_margin_top(label, 18);
    gtk_list_box_insert(GTK_LIST_BOX(app->results), label, -1);
    gtk_widget_show_all(app->results);
    GTask *task = g_task_new(app->window, NULL, search_done, app);
    g_task_set_task_data(task, job, search_job_free);
    g_task_run_in_thread(task, search_worker);
    g_object_unref(task);
}

static void search_clicked(GtkButton *button, gpointer user_data) {
    home_search((AppState *)user_data);
    (void)button;
}

static void search_enter(GtkEntry *entry, gpointer user_data) {
    home_search((AppState *)user_data);
    (void)entry;
}

static GtkWidget *grid_label(const char *text) {
    GtkWidget *label = gtk_label_new(text);
    gtk_widget_set_halign(label, GTK_ALIGN_START);
    return label;
}

void home_show_settings(AppState *app) {
    GtkWidget *dialog = gtk_dialog_new_with_buttons("Settings", GTK_WINDOW(app->window), GTK_DIALOG_MODAL,
        "Cancel", GTK_RESPONSE_CANCEL, "Save", GTK_RESPONSE_ACCEPT, NULL);
    GtkWidget *content = gtk_dialog_get_content_area(GTK_DIALOG(dialog));
    GtkWidget *grid = gtk_grid_new();
    GtkWidget *backend = gtk_combo_box_text_new();
    GtkWidget *instance = gtk_entry_new();
    GtkWidget *theme = gtk_combo_box_text_new();
    GtkWidget *hint = gtk_label_new("Built-in scraping avoids an API instance. YouTube may change its endpoints; Invidious is a fallback.");
    gtk_grid_set_row_spacing(GTK_GRID(grid), 12);
    gtk_grid_set_column_spacing(GTK_GRID(grid), 14);
    gtk_widget_set_margin_start(grid, 18);
    gtk_widget_set_margin_end(grid, 18);
    gtk_widget_set_margin_top(grid, 18);
    gtk_widget_set_margin_bottom(grid, 14);
    gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(backend), "local", "Built-in scraper");
    gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(backend), "invidious", "Invidious API");
    gtk_combo_box_set_active_id(GTK_COMBO_BOX(backend), app->config.backend);
    gtk_entry_set_text(GTK_ENTRY(instance), app->config.instance);
    gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(theme), "amoled", "AMOLED black");
    gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(theme), "white", "Simple white");
    gtk_combo_box_set_active_id(GTK_COMBO_BOX(theme), app->config.theme);
    gtk_label_set_line_wrap(GTK_LABEL(hint), TRUE);
    gtk_widget_set_size_request(hint, 380, -1);
    gtk_style_context_add_class(gtk_widget_get_style_context(hint), "dim-label");
    gtk_grid_attach(GTK_GRID(grid), grid_label("Data source"), 0, 0, 1, 1);
    gtk_grid_attach(GTK_GRID(grid), backend, 1, 0, 1, 1);
    gtk_grid_attach(GTK_GRID(grid), grid_label("Invidious instance"), 0, 1, 1, 1);
    gtk_grid_attach(GTK_GRID(grid), instance, 1, 1, 1, 1);
    gtk_grid_attach(GTK_GRID(grid), grid_label("Theme"), 0, 2, 1, 1);
    gtk_grid_attach(GTK_GRID(grid), theme, 1, 2, 1, 1);
    gtk_grid_attach(GTK_GRID(grid), hint, 0, 3, 2, 1);
    gtk_box_pack_start(GTK_BOX(content), grid, TRUE, TRUE, 0);
    gtk_widget_show_all(dialog);
    if (gtk_dialog_run(GTK_DIALOG(dialog)) == GTK_RESPONSE_ACCEPT) {
        const char *chosen_backend = gtk_combo_box_get_active_id(GTK_COMBO_BOX(backend));
        const char *chosen_theme = gtk_combo_box_get_active_id(GTK_COMBO_BOX(theme));
        const char *chosen_instance = gtk_entry_get_text(GTK_ENTRY(instance));
        g_strlcpy(app->config.backend, chosen_backend ? chosen_backend : "local", sizeof(app->config.backend));
        g_strlcpy(app->config.theme, chosen_theme ? chosen_theme : "amoled", sizeof(app->config.theme));
        g_strlcpy(app->config.instance, chosen_instance && *chosen_instance ? chosen_instance : "https://yewtu.be", sizeof(app->config.instance));
        if (!g_str_has_prefix(app->config.instance, "https://") && !g_str_has_prefix(app->config.instance, "http://")) {
            gchar *fixed = g_strdup_printf("https://%s", app->config.instance);
            g_strlcpy(app->config.instance, fixed, sizeof(app->config.instance));
            g_free(fixed);
        }
        config_save(&app->config);
        config_apply_theme(app->window, app->config.theme);
        gtk_widget_queue_draw(app->window);
        set_status(app, "Settings saved.");
    }
    gtk_widget_destroy(dialog);
}

static void settings_clicked(GtkButton *button, gpointer user_data) {
    home_show_settings((AppState *)user_data);
    (void)button;
}

GtkWidget *home_build(AppState *app) {
    GtkWidget *outer = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    GtkWidget *header = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    GtkWidget *brand_stack = gtk_box_new(GTK_ORIENTATION_VERTICAL, 2);
    GtkWidget *brand = gtk_label_new("BareTube");
    GtkWidget *subtitle = gtk_label_new("Just search. Just play. Keep no history.");
    GtkWidget *settings = gtk_button_new_with_label("Settings");
    GtkWidget *search_line = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    GtkWidget *search = gtk_entry_new();
    GtkWidget *scroll = gtk_scrolled_window_new(NULL, NULL);
    app->results = gtk_list_box_new();
    app->status = gtk_label_new("Ready · built-in scraper selected");
    app->search_entry = search;
    app->search_button = gtk_button_new_with_label("Search");

    gtk_widget_set_margin_start(outer, 16);
    gtk_widget_set_margin_end(outer, 16);
    gtk_widget_set_margin_top(outer, 14);
    gtk_widget_set_margin_bottom(outer, 10);
    gtk_widget_set_margin_bottom(header, 4);
    gtk_label_set_xalign(GTK_LABEL(brand), 0.0f);
    gtk_label_set_xalign(GTK_LABEL(subtitle), 0.0f);
    gtk_style_context_add_class(gtk_widget_get_style_context(subtitle), "dim-label");
    gtk_widget_set_halign(brand_stack, GTK_ALIGN_START);
    gtk_box_pack_start(GTK_BOX(brand_stack), brand, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(brand_stack), subtitle, FALSE, FALSE, 0);
    gtk_widget_set_hexpand(brand_stack, TRUE);
    gtk_box_pack_start(GTK_BOX(header), brand_stack, TRUE, TRUE, 0);
    gtk_box_pack_end(GTK_BOX(header), settings, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(outer), header, FALSE, FALSE, 0);

    gtk_entry_set_placeholder_text(GTK_ENTRY(search), "Search YouTube…");
    gtk_entry_set_activates_default(GTK_ENTRY(search), TRUE);
    gtk_widget_set_hexpand(search, TRUE);
    gtk_box_pack_start(GTK_BOX(search_line), search, TRUE, TRUE, 0);
    gtk_box_pack_start(GTK_BOX(search_line), app->search_button, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(outer), search_line, FALSE, FALSE, 0);

    gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(scroll), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
    gtk_container_add(GTK_CONTAINER(scroll), app->results);
    gtk_widget_set_vexpand(scroll, TRUE);
    gtk_box_pack_start(GTK_BOX(outer), scroll, TRUE, TRUE, 0);
    gtk_widget_set_halign(app->status, GTK_ALIGN_START);
    gtk_style_context_add_class(gtk_widget_get_style_context(app->status), "dim-label");
    gtk_box_pack_end(GTK_BOX(outer), app->status, FALSE, FALSE, 0);

    GtkWidget *welcome = gtk_label_new("No feed, subscriptions, likes, or watch history. Search above to get going.");
    gtk_label_set_line_wrap(GTK_LABEL(welcome), TRUE);
    gtk_widget_set_margin_top(welcome, 24);
    gtk_widget_set_margin_bottom(welcome, 24);
    gtk_list_box_insert(GTK_LIST_BOX(app->results), welcome, -1);
    g_signal_connect(app->search_button, "clicked", G_CALLBACK(search_clicked), app);
    g_signal_connect(search, "activate", G_CALLBACK(search_enter), app);
    g_signal_connect(settings, "clicked", G_CALLBACK(settings_clicked), app);
    return outer;
}
