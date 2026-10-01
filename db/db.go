package db

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

type Team struct {
	ID      int
	Members []string
	Variant int
}

type Database struct {
	DB *sql.DB
}

func InitDB(filepath string) (*Database, error) {
	db, err := sql.Open("sqlite", filepath)
	if err != nil {
		return nil, err
	}

	query := `
	CREATE TABLE IF NOT EXISTS chats (
		chat_id INTEGER PRIMARY KEY,
		state TEXT DEFAULT 'IDLE'
	);
	CREATE TABLE IF NOT EXISTS teams (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id INTEGER,
		variant INTEGER DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS members (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		team_id INTEGER,
		fio TEXT
	);
	`
	_, err = db.Exec(query)
	return &Database{DB: db}, err
}

// GetChatState теперь гарантированно создает запись, если ее не было
func (d *Database) GetChatState(chatID int64) (string, error) {
	var state string
	err := d.DB.QueryRow("SELECT state FROM chats WHERE chat_id = ?", chatID).Scan(&state)
	if err == sql.ErrNoRows {
		_, err = d.DB.Exec("INSERT INTO chats (chat_id, state) VALUES (?, 'IDLE') ON CONFLICT(chat_id) DO NOTHING", chatID)
		return "IDLE", err
	}
	return state, err
}

// SetChatState использует логику UPSERT (создать или обновить)
func (d *Database) SetChatState(chatID int64, state string) error {
	_, err := d.DB.Exec("INSERT INTO chats (chat_id, state) VALUES (?, ?) ON CONFLICT(chat_id) DO UPDATE SET state = ?", chatID, state, state)
	return err
}

func (d *Database) GetAllChats() ([]int64, error) {
	rows, err := d.DB.Query("SELECT chat_id FROM chats")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			chats = append(chats, id)
		}
	}
	return chats, nil
}

func (d *Database) CreateTeamWithMembers(chatID int64, members []string) error {
	res, err := d.DB.Exec("INSERT INTO teams (chat_id) VALUES (?)", chatID)
	if err != nil {
		return err
	}

	teamID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	for _, fio := range members {
		_, err = d.DB.Exec("INSERT INTO members (team_id, fio) VALUES (?, ?)", teamID, fio)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Database) GetTeamMembers(teamID int) ([]string, error) {
	rows, err := d.DB.Query("SELECT fio FROM members WHERE team_id = ?", teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var fio string
		if err := rows.Scan(&fio); err == nil {
			members = append(members, fio)
		}
	}
	return members, nil
}

func (d *Database) GetAllTeams(chatID int64) ([]Team, error) {
	rows, err := d.DB.Query("SELECT id, variant FROM teams WHERE chat_id = ?", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Variant); err == nil {
			members, _ := d.GetTeamMembers(t.ID)
			if len(members) > 0 {
				t.Members = members
				teams = append(teams, t)
			}
		}
	}
	return teams, nil
}

func (d *Database) SaveTeamVariant(teamID int, variant int) error {
	_, err := d.DB.Exec("UPDATE teams SET variant = ? WHERE id = ?", variant, teamID)
	return err
}

func (d *Database) ClearTeams(chatID int64) error {
	_, err := d.DB.Exec("DELETE FROM members WHERE team_id IN (SELECT id FROM teams WHERE chat_id = ?)", chatID)
	if err != nil {
		return err
	}
	_, err = d.DB.Exec("DELETE FROM teams WHERE chat_id = ?", chatID)
	return err
}
