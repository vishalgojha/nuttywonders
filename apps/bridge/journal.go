package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const journalSelect = "id,slug,title,eyebrow,category,excerpt,body,image_path,is_published,published_at,created_at,updated_at"

func (s *Server) handleListJournal(w http.ResponseWriter, r *http.Request) {
	query := url.Values{}
	query.Set("select", journalSelect)
	query.Set("is_published", "eq.true")
	query.Set("order", "published_at.desc,created_at.desc")
	var articles []JournalArticle
	if err := s.db.Select(r.Context(), "journal_articles", query, &articles); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": articles})
}

func (s *Server) handleGetJournal(w http.ResponseWriter, r *http.Request) {
	query := url.Values{}
	query.Set("select", journalSelect)
	query.Set("slug", "eq."+r.PathValue("slug"))
	query.Set("is_published", "eq.true")
	query.Set("limit", "1")
	var articles []JournalArticle
	if err := s.db.Select(r.Context(), "journal_articles", query, &articles); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(articles) == 0 {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"article": articles[0]})
}

func (s *Server) handleAdminListJournal(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	query := url.Values{}
	query.Set("select", journalSelect)
	query.Set("order", "updated_at.desc")
	var articles []JournalArticle
	if err := s.db.Select(r.Context(), "journal_articles", query, &articles); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": articles})
}

type journalInput struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Eyebrow     string `json:"eyebrow"`
	Category    string `json:"category"`
	Excerpt     string `json:"excerpt"`
	Body        string `json:"body"`
	ImagePath   string `json:"image_path"`
	IsPublished bool   `json:"is_published"`
}

func (s *Server) handleAdminSaveJournal(w http.ResponseWriter, r *http.Request, _ adminIdentity) {
	var input journalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Slug = slugify(strings.TrimSpace(input.Slug), "")
	if input.Slug == "" {
		input.Slug = slugify(input.Title, "article")
	}
	if input.Title == "" || input.Slug == "" {
		writeError(w, http.StatusBadRequest, "title and slug are required")
		return
	}
	if input.Eyebrow == "" {
		input.Eyebrow = "Snack notes"
	}
	if input.Category == "" {
		input.Category = input.Eyebrow
	}
	row := map[string]any{
		"slug": input.Slug, "title": input.Title, "eyebrow": input.Eyebrow,
		"category": input.Category, "excerpt": input.Excerpt, "body": input.Body,
		"image_path": nullableString(input.ImagePath), "is_published": input.IsPublished,
	}
	if input.IsPublished {
		row["published_at"] = time.Now().UTC()
	}
	var saved []JournalArticle
	var err error
	if input.ID > 0 {
		query := url.Values{}
		query.Set("id", "eq."+strconv.FormatInt(input.ID, 10))
		err = s.db.Update(r.Context(), "journal_articles", query, row, &saved)
	} else {
		err = s.db.Insert(r.Context(), "journal_articles", row, &saved)
	}
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if len(saved) == 0 {
		writeError(w, http.StatusInternalServerError, "article was not saved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"article": saved[0]})
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
