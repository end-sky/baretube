#ifndef BARETUBE_HOME_H
#define BARETUBE_HOME_H

#include <gtk/gtk.h>
#include "config.h"

typedef struct {
    GtkWidget *window;
    GtkWidget *search_entry;
    GtkWidget *search_button;
    GtkWidget *results;
    GtkWidget *status;
    AppConfig config;
    char *backend_path;
    char *mpv_path;
    guint generation;
} AppState;

GtkWidget *home_build(AppState *app);
void home_search(AppState *app);
void home_show_settings(AppState *app);

#endif
