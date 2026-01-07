{{define "fts"}}
{{- $tokenizer := (or .tokenizer "unicode61") -}}
{{- if or (not .cols) (not .table) (not .name) }} {{ panic "table, name & cols are required" }} {{ end -}}
CREATE VIRTUAL TABLE {{ .name }} USING
  fts5({{ range $i, $col := .cols }}{{ if $i }}, {{ end }}{{ $col }}{{ end -}},
  content='{{ .table }}', content_rowid='{{ .id }}', tokenize='{{ $tokenizer }}');

CREATE VIRTUAL TABLE {{ .name}}_rows USING fts5vocab('{{ .name }}', 'row');
CREATE VIRTUAL TABLE {{ .name}}_instances USING fts5vocab('{{ .name }}', 'instance');
CREATE VIRTUAL TABLE {{ .name}}_cols USING fts5vocab('{{ .name }}', 'col');

CREATE TRIGGER {{ .name }}_ai AFTER INSERT ON {{ .table }} BEGIN
  INSERT INTO {{ .name }}(rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}{{ $col }}{{ end }})
    VALUES (new.rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}new.{{ $col }}{{ end }});
END;

CREATE TRIGGER {{ .name }}_ad AFTER DELETE ON {{ .table }} BEGIN
  INSERT INTO {{ .name }}({{ .name }}, rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}{{ $col }}{{ end }})
    VALUES ('delete', old.rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}old.{{ $col }}{{ end }});
END;

CREATE TRIGGER {{ .name }}_au AFTER UPDATE ON {{ .table }} BEGIN
  INSERT INTO {{ .name }}({{ .name }}, rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}{{ $col }}{{ end }})
    VALUES ('delete', old.rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}old.{{ $col }}{{ end }});
  INSERT INTO {{ .name }}(rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}{{ $col }}{{ end }})
    VALUES (new.rowid, {{ range $i, $col := .cols}}{{ if $i }}, {{ end }}new.{{ $col }}{{ end }});
END;
{{ end }}


{{ define "schema" }}
{{ $t := . }}
CREATE TABLE IF NOT EXISTS {{ $t.name }}s (
  {{- range $i, $f := $t.fields }}
    {{ if $i }}, {{ end }}
      {{- $f.Name }} {{ $f.Kind }} {{ $f.Extra }}
        {{- if $f.Fallback }} DEFAULT '{{ $f.Fallback }}' {{ end }}
  {{- end }}
  {{- if $t.rest }}, {{ $t.rest }} {{ end -}}
);
{{ .raw }}
{{ end }}


{{define "schema-field-default"}}
{{ if eq .on "create" }}
CREATE TRIGGER {{ .table }}_{{ .field }}_default_ai AFTER INSERT ON {{ .table }}s
WHEN NEW.{{ .field }} = {{ .when }} OR NEW.{{ .field }} IS NULL
BEGIN UPDATE {{ .table }}s SET {{ .field }} = {{ .default }} WHERE rowid = NEW.rowid; END;
CREATE TRIGGER {{ .table }}_{{ .field }}_default_au AFTER UPDATE ON {{ .table }}s
WHEN NEW.{{ .field }} = {{ .when }}
BEGIN UPDATE {{ .table }}s SET {{ .field }} = OLD.{{ .field }} WHERE rowid = NEW.rowid; END;
{{ end }}
{{ if eq .on "update" }}
CREATE TRIGGER {{ .table }}_{{ .field }}_default_au AFTER UPDATE ON {{ .table }}s
WHEN NEW.{{ .field }} = {{ .when }} OR OLD.{{ .field }} IS NEW.{{ .field }}
BEGIN UPDATE {{ .table }}s SET {{ .field }} = {{ .default }} WHERE rowid = NEW.rowid; END;
{{ end }}
{{ end }}
