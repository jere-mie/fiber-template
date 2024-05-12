package main

import (
	"encoding/gob"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/session"
	"github.com/gofiber/template/django/v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// User model
type User struct {
	gorm.Model
	Username string
	Password string
}

func main() {
	// register type for flash messages
	gob.Register([]map[string]string{})

	// Initialize the HTML template engine
	engine := django.New("./templates", ".html")
	sessionStore := session.New() // Create session store within main

	// Create a Fiber app with the configured engine
	app := fiber.New(fiber.Config{
		Views:             engine,
		PassLocalsToViews: true,
	})

	app.Use(logger.New())

	// Serve static files
	app.Static("/static", "./static")

	// Setup Database
	db, err := gorm.Open(sqlite.Open("site.db"), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}
	// Migrate the schema
	db.AutoMigrate(&User{})

	app.Use(authMiddleware(sessionStore, db))

	// Setup routes
	setupRoutes(app, db, sessionStore)

	// Start the Fiber application
	log.Fatal(app.Listen(":3000"))
}

func setupRoutes(app *fiber.App, db *gorm.DB, sessionStore *session.Store) {
	app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("index", prepareTemplateData(c, nil, sessionStore))
	})

	app.Get("/register", func(c *fiber.Ctx) error {
		return c.Render("register", prepareTemplateData(c, nil, sessionStore))
	})

	app.Get("/login", func(c *fiber.Ctx) error {
		return c.Render("login", prepareTemplateData(c, nil, sessionStore))
	})

	app.Post("/register", func(c *fiber.Ctx) error {
		// Parse the form
		var data struct {
			Username string `form:"username"`
			Password string `form:"password"`
		}
		if err := c.BodyParser(&data); err != nil {
			return err
		}

		// check length of username and password
		if len(data.Username) < 5 || len(data.Password) < 5 {
			flash(c, "Username and Password must be 5 characters or greater", "danger", sessionStore)
			return c.Render("register", prepareTemplateData(c, nil, sessionStore))
		}

		// Check if username already exists
		var user User
		result := db.Where("username = ?", data.Username).First(&user)
		if result.Error == nil {
			flash(c, "User already exists", "danger", sessionStore)
			return c.Render("register", prepareTemplateData(c, nil, sessionStore))
		}

		// Create new user
		newUser := User{Username: data.Username, Password: data.Password}
		db.Create(&newUser)

		flash(c, "Registration successful!", "success", sessionStore)

		// Redirect to the homepage
		return c.Redirect("/")
	})

	app.Post("/login", func(c *fiber.Ctx) error {
		var data struct {
			Username string `form:"username"`
			Password string `form:"password"`
		}
		if err := c.BodyParser(&data); err != nil {
			return err
		}

		// Authenticate user
		var user User
		if err := db.Where("username = ? AND password = ?", data.Username, data.Password).First(&user).Error; err != nil {
			flash(c, "Invalid username or password", "danger", sessionStore)
			return c.Redirect("/login")
		}

		// Create session and store only user_id
		sess, err := sessionStore.Get(c)
		if err != nil {
			return err
		}
		sess.Set("user_id", user.ID)
		if err := sess.Save(); err != nil {
			return err
		}

		// Optionally set a cookie with session ID, though Fiber does this automatically
		c.Cookie(&fiber.Cookie{
			Name:     "session_id",
			Value:    sess.ID(),
			Expires:  time.Now().Add(1 * time.Hour),
			HTTPOnly: true,
		})

		flash(c, "Login successful!", "success", sessionStore)
		return c.Redirect("/")
	})

	app.Get("/logout", func(c *fiber.Ctx) error {
		sess, err := sessionStore.Get(c)
		if err != nil {
			return err
		}
		// Destroy the session
		sess.Destroy()

		// Clear the cookie
		c.ClearCookie("session_id")

		flash(c, "Logout successful", "success", sessionStore)
		return c.Redirect("/")
	})

	app.Get("/api/users", func(c *fiber.Ctx) error {
		var users []User
		db.Find(&users)
		return c.JSON(users)
	})
}

func prepareTemplateData(c *fiber.Ctx, data fiber.Map, sessionStore *session.Store) fiber.Map {
	if data == nil {
		data = fiber.Map{}
	}

	// Load session
	sess, err := sessionStore.Get(c)
	if err != nil {
		log.Println("Error fetching session:", err)
		return data
	}

	// Retrieve and include flashes if available
	flashes := sess.Get("flashes")
	if flashes != nil {
		data["Flashes"] = flashes
		sess.Delete("flashes") // Optionally clear flashes after loading
		sess.Save()
	}

	return data
}

func flash(c *fiber.Ctx, message string, category string, sessionStore *session.Store) error {
	sess, err := sessionStore.Get(c)
	if err != nil {
		return err
	}
	var flashes []map[string]string
	if f := sess.Get("flashes"); f != nil {
		flashes = f.([]map[string]string)
	} else {
		flashes = make([]map[string]string, 0) // Explicit initialization
	}
	flashes = append(flashes, map[string]string{"message": message, "category": category})
	sess.Set("flashes", flashes)
	if err := sess.Save(); err != nil {
		log.Println("Error saving session:", err)
	}
	return err
}

func authMiddleware(sessionStore *session.Store, db *gorm.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		log.Println("Querying user")
		sess, err := sessionStore.Get(c)
		if err != nil {
			log.Println("Error fetching session:", err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		userID := sess.Get("user_id")
		if userID == nil {
			log.Println("user_id is nil")
			return c.Next()
		}

		var user User
		if err := db.First(&user, userID).Error; err != nil {
			log.Println("User not found:", err)
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		log.Println("User found and set", user.Username)

		c.Locals("user", &user)
		return c.Next()
	}
}

func getCurrentUser(c *fiber.Ctx, sessionStore *session.Store, db *gorm.DB) *User {
	// Retrieve the session using the Fiber context
	sess, err := sessionStore.Get(c)
	if err != nil {
		log.Println("Error fetching session:", err)
		return nil
	}

	// Attempt to retrieve user_id from the session
	userID, ok := sess.Get("user_id").(uint)
	if !ok {
		return nil // No user_id found, or type assertion failed
	}

	// Retrieve the user from the database based on user_id
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		log.Println("User not found:", err)
		return nil
	}

	return &user
}
