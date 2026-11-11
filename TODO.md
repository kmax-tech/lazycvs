# TODO

## Notiert, nicht geplant

- **Tasten-Audit vom 2026-11-11** (Bericht: `issues/025-keymap-audit-2026-11-11.md`, Stand Commit `4292f14`). 61 Tasten in `tui/keys.go` und `tui/keymap.go`, 20 direkte Tastenabfragen daran vorbei, 33 Widersprüche zwischen Handlern, Tastenleiste, Hilfe, README und `docs/USER_GUIDE.md`. Die Nummern sind die der Drift-Liste im Bericht. Was davon erledigt ist, steht in den Commits desselben Tages; offen bleibt, was hier darunter steht.
- **Server-seitige Ignore-Liste fehlt in der Ignore-Logik.** cvs(5) nennt
  `$CVSROOT/CVSROOT/cvsignore` als vierte Quelle; bei `:ext:`-Repos fragt
  der Client den Server per `Questionable`. `globalIgnorePatterns`
  (`tui/app_files.go`) liest nur Defaults, `~/.cvsignore`, `$CVSIGNORE`
  und die `.cvsignore` des Verzeichnisses. Stehen am Server Muster, zeigt
  lazycvs Dateien als `?`, die `cvs -n update` verschweigt. Lösung bräuchte
  einen `cvs co -p CVSROOT/cvsignore` beim Start (Netzzugriff, gecacht).
  Notiert 2026-10-27, offen: ob der Webis-Server überhaupt eine hat.
- **`markedBulkIgnore` schluckt Schreibfehler.** Ein fehlgeschlagener
  `.cvsignore`-Write behält zwar die Markierung, erscheint aber weder in
  der Console noch als Meldung (`tui/app_actions.go`, Fail-loud-Regel).
