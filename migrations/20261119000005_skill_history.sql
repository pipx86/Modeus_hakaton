-- +goose Up
CREATE TABLE skill_frequency_snapshots (
    series TEXT NOT NULL,
    snapshot_month DATE NOT NULL CHECK (EXTRACT(DAY FROM snapshot_month) = 1),
    frequencies JSONB NOT NULL CHECK (jsonb_typeof(frequencies) = 'object'),
    PRIMARY KEY (series, snapshot_month)
);

-- +goose Down
DROP TABLE skill_frequency_snapshots;
