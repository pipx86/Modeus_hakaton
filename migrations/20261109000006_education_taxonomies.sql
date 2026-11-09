-- +goose Up
-- Справочники и их элементы. Код необязателен: не у всех источников он есть.
CREATE TABLE taxonomies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id UUID REFERENCES sources(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    version TEXT CHECK (version IS NULL OR btrim(version) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_taxonomies_source ON taxonomies(source_id);

CREATE TABLE taxonomy_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    taxonomy_id UUID NOT NULL REFERENCES taxonomies(id) ON DELETE CASCADE,
    parent_id UUID,
    code TEXT CHECK (code IS NULL OR btrim(code) <> ''),
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (taxonomy_id, id),
    FOREIGN KEY (taxonomy_id, parent_id) REFERENCES taxonomy_items(taxonomy_id, id),
    CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE UNIQUE INDEX uq_taxonomy_items_code ON taxonomy_items(taxonomy_id, code) WHERE code IS NOT NULL;
CREATE INDEX idx_taxonomy_items_parent ON taxonomy_items(taxonomy_id, parent_id);

CREATE TABLE education_programs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    organization TEXT CHECK (organization IS NULL OR btrim(organization) <> ''),
    code TEXT CHECK (code IS NULL OR btrim(code) <> ''),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE program_sources (
    program_id UUID NOT NULL REFERENCES education_programs(id) ON DELETE CASCADE,
    source_id UUID NOT NULL REFERENCES sources(id) ON DELETE RESTRICT,
    PRIMARY KEY (program_id, source_id)
);
CREATE INDEX idx_program_sources_source ON program_sources(source_id);

CREATE TABLE program_competencies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    program_id UUID NOT NULL REFERENCES education_programs(id) ON DELETE CASCADE,
    code TEXT CHECK (code IS NULL OR btrim(code) <> ''),
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_program_competencies_code ON program_competencies(program_id, code) WHERE code IS NOT NULL;
CREATE INDEX idx_program_competencies_program ON program_competencies(program_id);

-- KSA: знания, умения и навыки, связанные с компетенцией программы.
CREATE TABLE competency_ksa (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    competency_id UUID NOT NULL REFERENCES program_competencies(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('knowledge', 'skill', 'ability')),
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_competency_ksa_competency ON competency_ksa(competency_id);

-- Отсутствие строки связи означает, что элемент ещё не сопоставлен.
CREATE TABLE competency_taxonomy_links (
    competency_id UUID NOT NULL REFERENCES program_competencies(id) ON DELETE CASCADE,
    taxonomy_item_id UUID NOT NULL REFERENCES taxonomy_items(id) ON DELETE RESTRICT,
    PRIMARY KEY (competency_id, taxonomy_item_id)
);
CREATE INDEX idx_competency_taxonomy_links_item ON competency_taxonomy_links(taxonomy_item_id);

CREATE TABLE ksa_taxonomy_links (
    ksa_id UUID NOT NULL REFERENCES competency_ksa(id) ON DELETE CASCADE,
    taxonomy_item_id UUID NOT NULL REFERENCES taxonomy_items(id) ON DELETE RESTRICT,
    PRIMARY KEY (ksa_id, taxonomy_item_id)
);
CREATE INDEX idx_ksa_taxonomy_links_item ON ksa_taxonomy_links(taxonomy_item_id);

-- +goose Down
-- Откат удаляет все данные этих таблиц; перед ним нужна резервная копия.
DROP TABLE ksa_taxonomy_links;
DROP TABLE competency_taxonomy_links;
DROP TABLE competency_ksa;
DROP TABLE program_competencies;
DROP TABLE program_sources;
DROP TABLE education_programs;
DROP TABLE taxonomy_items;
DROP TABLE taxonomies;
