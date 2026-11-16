-- +goose Up
-- Родитель и дочерний узел должны принадлежать одному исследованию.
ALTER TABLE nodes ADD CONSTRAINT uq_nodes_id_run_id UNIQUE (id, run_id);
ALTER TABLE nodes ADD CONSTRAINT fk_nodes_parent_same_run
    FOREIGN KEY (parent_id, run_id) REFERENCES nodes (id, run_id)
    ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

-- Корень исследования должен быть узлом этого исследования.
ALTER TABLE runs ADD CONSTRAINT fk_runs_root_same_run
    FOREIGN KEY (root_node_id, id) REFERENCES nodes (id, run_id)
    DEFERRABLE INITIALLY DEFERRED;

-- +goose Down
ALTER TABLE runs DROP CONSTRAINT fk_runs_root_same_run;
ALTER TABLE nodes DROP CONSTRAINT fk_nodes_parent_same_run;
ALTER TABLE nodes DROP CONSTRAINT uq_nodes_id_run_id;
