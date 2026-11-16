// Пакет demofill создаёт явно помеченные демонстрационные данные.
package demofill

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"activity-descriptor/internal/store"
	"activity-descriptor/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const profession = "ДЕМО: аналитик данных"
const sourceTitle = "ДЕМО: синтетический источник, не реальный документ"

func identifier(key string) pgtype.UUID {
	sum := sha256.Sum256([]byte("modeus/demo-fixture/v1/" + key))
	var bytes [16]byte
	copy(bytes[:], sum[:16])
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return pgtype.UUID{Bytes: bytes, Valid: true}
}
func пример() (store.TreeNode, string) {
	source := identifier("source")
	lines := []string{"Это вымышленный учебный материал. Все формулировки синтетические."}
	makeNode := func(key, title string) store.TreeNode {
		quote := "Демонстрационная формулировка: " + title
		lines = append(lines, quote)
		return store.TreeNode{ID: identifier("node/" + key), Title: title, Evidence: []store.TreeEvidence{{ID: identifier("evidence/" + key), SourceID: source, Quote: quote}}}
	}
	root := makeNode("role", profession)
	functions := []struct {
		title string
		tasks []string
	}{
		{"Сбор данных", []string{"Определить источники", "Получить набор данных", "Проверить доступность", "Зафиксировать происхождение"}},
		{"Анализ данных", []string{"Проверить качество", "Подготовить данные", "Исследовать показатели", "Проверить результаты"}},
		{"Представление результатов", []string{"Выбрать показатели", "Подготовить визуализации", "Описать выводы", "Передать результат"}},
	}
	actions := []string{"Подготовить данные для задачи", "Выполнить действия по задаче", "Проверить результат задачи"}
	for i, fn := range functions {
		f := makeNode(fmt.Sprintf("function/%d", i), fn.title)
		for j, title := range fn.tasks {
			task := makeNode(fmt.Sprintf("task/%d/%d", i, j), title)
			for k, action := range actions {
				task.Children = append(task.Children, makeNode(fmt.Sprintf("action/%d/%d/%d", i, j, k), action))
			}
			f.Children = append(f.Children, task)
		}
		root.Children = append(root.Children, f)
	}
	return root, strings.Join(lines, "\n")
}

// Apply добавляет пример одной транзакцией. Повтор не изменяет существующие данные.
func Apply(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	if pool == nil {
		return false, fmt.Errorf("не задано подключение к базе")
	}
	root, content := пример()
	if err := store.ValidateTree(root); err != nil {
		return false, fmt.Errorf("проверить демодерево: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("начать добавление примера: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := db.New(tx)
	if err = q.LockDemoFixture(ctx); err != nil {
		return false, fmt.Errorf("заблокировать добавление примера: %w", err)
	}
	runID, sourceID := identifier("run"), identifier("source")
	count, err := q.CreateDemoRun(ctx, db.CreateDemoRunParams{ID: runID, Profession: profession})
	if err != nil {
		return false, fmt.Errorf("создать демоисследование: %w", err)
	}
	if count == 0 {
		if err := проверить(ctx, q, runID, sourceID, root, content); err != nil {
			return false, fmt.Errorf("существующие демоданные изменены, автоматическая замена отменена: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	_, err = q.CreateSource(ctx, db.CreateSourceParams{ID: sourceID, RunID: runID, Type: "document", Title: sourceTitle, Content: pgtype.Text{String: content, Valid: true}, ProcessingStatus: "processed"})
	if err != nil {
		return false, fmt.Errorf("создать демонстрационный источник: %w", err)
	}
	kinds := []string{"role", "function", "task", "action"}
	var save func(store.TreeNode, pgtype.UUID, int) error
	save = func(n store.TreeNode, parent pgtype.UUID, depth int) error {
		_, err := q.CreateNode(ctx, db.CreateNodeParams{ID: n.ID, RunID: runID, ParentID: parent, Level: int16(depth), Type: kinds[depth], Title: n.Title})
		if err != nil {
			return err
		}
		for _, e := range n.Evidence {
			if _, err := q.CreateEvidence(ctx, db.CreateEvidenceParams{ID: e.ID, NodeID: n.ID, SourceID: e.SourceID, Quote: e.Quote, Approved: e.Approved}); err != nil {
				return err
			}
		}
		for _, child := range n.Children {
			if err := save(child, n.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err = save(root, pgtype.UUID{}, 0); err != nil {
		return false, fmt.Errorf("сохранить демонстрационное дерево: %w", err)
	}
	if n, err := q.SetRunRoot(ctx, db.SetRunRootParams{ID: runID, RootNodeID: root.ID}); err != nil || n != 1 {
		return false, fmt.Errorf("сохранить корень примера: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("подтвердить демоданные: %w", err)
	}
	return true, nil
}

func проверить(ctx context.Context, q *db.Queries, runID, sourceID pgtype.UUID, root store.TreeNode, content string) error {
	run, err := q.GetDemoRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Profession != profession || run.RootNodeID != root.ID {
		return fmt.Errorf("исследование не совпадает с примером")
	}
	source, err := q.GetSource(ctx, sourceID)
	if err != nil {
		return err
	}
	if source.Title != sourceTitle || source.Type != "document" || !source.Content.Valid || source.Content.String != content {
		return fmt.Errorf("источник не совпадает с примером")
	}
	linked, err := q.ListSourcesByRun(ctx, runID)
	if err != nil {
		return err
	}
	found := false
	for _, s := range linked {
		if s.ID == sourceID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("нет связи с источником")
	}
	nodes, err := q.ListNodesByRun(ctx, runID)
	if err != nil {
		return err
	}
	if len(nodes) != 52 {
		return fmt.Errorf("ожидалось 52 узла, получено %d", len(nodes))
	}
	kinds := []string{"role", "function", "task", "action"}
	var check func(store.TreeNode, pgtype.UUID, int) error
	check = func(expected store.TreeNode, parent pgtype.UUID, depth int) error {
		actual, err := q.GetNode(ctx, expected.ID)
		if err != nil {
			return err
		}
		if actual.RunID != runID || actual.ParentID != parent || actual.Level != int16(depth) || actual.Type != kinds[depth] || actual.Title != expected.Title {
			return fmt.Errorf("узел не совпадает с примером")
		}
		evidence, err := q.ListEvidenceByNode(ctx, expected.ID)
		if err != nil {
			return err
		}
		if len(evidence) != 1 || evidence[0].ID != expected.Evidence[0].ID || evidence[0].SourceID != sourceID || evidence[0].Quote != expected.Evidence[0].Quote || evidence[0].Approved {
			return fmt.Errorf("подтверждение не совпадает с примером")
		}
		for _, child := range expected.Children {
			if err := check(child, expected.ID, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return check(root, pgtype.UUID{}, 0)
}
