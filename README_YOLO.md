# AI Recipe Recommendation System using YOLO

![Python](https://img.shields.io/badge/Python-3.10%2B-blue?logo=python)
![YOLOv11](https://img.shields.io/badge/YOLO-v11-orange)
![Groq](https://img.shields.io/badge/Groq-Llama%203-purple)
![Streamlit](https://img.shields.io/badge/Streamlit-1.x-red?logo=streamlit)
![OpenCV](https://img.shields.io/badge/OpenCV-4.x-green?logo=opencv)
![License](https://img.shields.io/badge/License-MIT-lightgrey)

> An end-to-end computer vision pipeline that detects food ingredients from a live camera feed and generates personalized, context-aware recipe recommendations in milliseconds — powered by YOLOv11 and the Groq API (Llama 3).

---

## Table of Contents

- [Overview](#overview)
- [Key Features](#key-features)
- [System Architecture](#system-architecture)
- [Tech Stack](#tech-stack)
- [User Preference Engine](#user-preference-engine)
- [Project Structure](#project-structure)
- [Dataset & Training Guide](#dataset--training-guide)
- [Setup & Installation](#setup--installation)
- [Running the App](#running-the-app)
- [API Reference](#api-reference)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

---

## Overview

This project bridges **computer vision** and **generative AI** to solve a real-world problem: *"What can I cook with what I have right now?"*

Point your camera at ingredients on your kitchen counter. The system detects them in real time using YOLOv11, cross-references your personal dietary profile, and calls Groq's ultra-fast inference API to generate tailored recipe suggestions — all in under a second.

---

## Key Features

| Feature | Description |
|---|---|
| **Real-time Ingredient Detection** | YOLOv11 identifies food items from a live webcam feed via OpenCV |
| **Context-Aware Recipe Generation** | Groq API (Llama 3) crafts structured recipes using detected ingredients and user preferences |
| **User Preference Engine** | 5-dimensional preference profile: diet, health, cuisine, mood, time |
| **Streamlit Web UI** | Clean browser interface with live video, ingredient overlay, and recipe cards |
| **Sub-second Inference** | Groq's LPU-powered inference delivers recipes in ~200–400ms |
| **Extensible Preference System** | Easily add new dimensions (allergies, calorie targets, serving size) |

---

## System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Streamlit Web App                        │
│  ┌───────────────────┐              ┌──────────────────────┐    │
│  │   Webcam Feed     │              │    Recipe Display     │    │
│  │   (OpenCV)        │              │    (Cards + Steps)   │    │
│  └────────┬──────────┘              └──────────┬───────────┘    │
└───────────┼──────────────────────────────────── ┼───────────────┘
            │                                      │
            ▼                                      │
┌───────────────────────┐              ┌───────────────────────────┐
│   YOLOv11 Detector    │              │      Groq API             │
│                       │──────────────▶   (Llama 3 - 70B)        │
│  Input: Video frames  │  Detected    │                           │
│  Output: Ingredient   │  Ingredients │  Input:  Ingredients +    │
│          labels +     │  + User      │          User Profile     │
│          bounding     │  Profile     │  Output: Structured       │
│          boxes        │              │          Recipe JSON       │
└───────────────────────┘              └───────────────────────────┘
            ▲
            │
┌───────────────────────┐
│  User Preference      │
│  Engine               │
│                       │
│  - Diet type          │
│  - Health condition   │
│  - Cuisine preference │
│  - Current mood       │
│  - Time constraint    │
└───────────────────────┘
```

### Data Flow

```
Camera Frame
    │
    ▼
OpenCV (frame preprocessing)
    │
    ▼
YOLOv11 (inference)
    │
    ▼
Detected Ingredients List  ◄──  User Preference Profile
    │                                    │
    └──────────────┬─────────────────────┘
                   │
                   ▼
         Prompt Builder (constructs structured LLM prompt)
                   │
                   ▼
         Groq API / Llama 3
                   │
                   ▼
         Recipe JSON (name, ingredients, steps, nutrition estimate)
                   │
                   ▼
         Streamlit UI (rendered recipe cards)
```

---

## Tech Stack

| Component | Technology | Purpose |
|---|---|---|
| Object Detection | [YOLOv11](https://github.com/ultralytics/ultralytics) | Real-time food ingredient detection |
| Video Capture | [OpenCV](https://opencv.org/) | Webcam feed and frame preprocessing |
| LLM Inference | [Groq API](https://console.groq.com/) + Llama 3 (70B) | Context-aware recipe generation |
| Web Interface | [Streamlit](https://streamlit.io/) | Interactive UI with live video stream |
| Language | Python 3.10+ | Core application language |
| Model Training | [Ultralytics CLI](https://docs.ultralytics.com/) | Fine-tuning YOLO on custom food dataset |
| Data Labeling | [Roboflow](https://roboflow.com/) | Dataset annotation and augmentation |

---

## User Preference Engine

The preference engine builds a structured profile that is injected into every Groq prompt. This ensures recipes are not just ingredient-driven but truly personalized.

### Preference Dimensions

#### 1. Diet Type
| Option | Description |
|---|---|
| `veg` | Strictly vegetarian — no meat, fish, or eggs |
| `non-veg` | All ingredients allowed |
| `eggetarian` | Vegetarian + eggs, no meat or fish |

#### 2. Health Condition
| Option | Dietary Impact |
|---|---|
| `normal` | No restrictions |
| `diabetic` | Low GI ingredients, reduced sugar/refined carbs |
| `low_bp` | Salt-aware; includes sodium-boosting ingredients where appropriate |
| `high_bp` | Strictly low sodium, heart-healthy fats |

#### 3. Cuisine Preference
| Option | Style |
|---|---|
| `north_indian` | Curries, dals, bread-based, ghee-rich |
| `south_indian` | Rice-based, coconut, tamarind, idli/dosa variants |
| `chinese` | Stir-fry, soy-sauce based, noodle/rice dishes |
| `mediterranean` | Olive oil, legumes, grains, fresh herbs |
| `japanese` | Umami-forward, minimal oil, clean presentation |

#### 4. Mood
| Option | Recipe Style |
|---|---|
| `party` | Impressive, multi-component, crowd-pleasing |
| `tired` | 1-pot, minimal steps, comfort food |
| `romantic` | Plated, elegant, moderate complexity |
| `quick_bite` | Snack-level, 1–2 ingredients, no cooking required |

#### 5. Time Constraint
| Option | Max cook time |
|---|---|
| `10` | Under 10 minutes |
| `20` | Under 20 minutes |
| `30` | Under 30 minutes |
| `40` | Under 40 minutes |

### Example Preference Profile (JSON)

```json
{
  "diet": "eggetarian",
  "health": "diabetic",
  "cuisine": "south_indian",
  "mood": "tired",
  "time_minutes": 20
}
```

---

## Project Structure

```
yolo/
├── app.py                      # Streamlit entry point
├── requirements.txt            # Python dependencies
├── .env                        # API keys (Groq, etc.) — never commit this
├── .gitignore
│
├── detector/
│   ├── __init__.py
│   ├── yolo_detector.py        # YOLOv11 inference wrapper
│   └── frame_processor.py     # OpenCV webcam capture + preprocessing
│
├── preference_engine/
│   ├── __init__.py
│   ├── schema.py               # Pydantic models for user preferences
│   └── preference_ui.py       # Streamlit sidebar widgets for preferences
│
├── recipe_engine/
│   ├── __init__.py
│   ├── prompt_builder.py       # Builds structured prompts from ingredients + preferences
│   ├── groq_client.py          # Groq API wrapper (async-ready)
│   └── recipe_parser.py       # Parses and validates LLM JSON output
│
├── ui/
│   ├── components.py           # Reusable Streamlit components (recipe cards, overlays)
│   └── styles.css              # Custom CSS for Streamlit
│
├── models/
│   └── food_yolo11/            # Fine-tuned YOLO weights (not committed — use .gitignore)
│       ├── best.pt
│       └── last.pt
│
├── data/
│   ├── classes.txt             # List of detectable ingredient classes
│   └── dataset.yaml            # YOLO training dataset config
│
└── training/
    ├── train.py                # Training script
    ├── evaluate.py             # Model evaluation on test set
    └── export.py               # Export model to ONNX / TorchScript
```

---

## Dataset & Training Guide

### Step 1: Choose a Base Dataset

Start with one of these publicly available food datasets, then extend with custom images:

| Dataset | Classes | Source |
|---|---|---|
| Open Images V7 (Food subset) | 50+ food categories | [Open Images](https://storage.googleapis.com/openimages/web/index.html) |
| Food-101 | 101 food types | [Kaggle](https://www.kaggle.com/datasets/dansbecker/food-101) |
| VegFru | 292 vegetable/fruit types | [GitHub](https://github.com/ustc-vim/vegfru) |
| Custom Capture | Your specific ingredients | Webcam + labeling tool |

**Recommended approach:** Start with Open Images for common ingredients, then shoot 50–100 images per any missing class with your own webcam.

### Step 2: Annotate with Roboflow

1. Create a free project at [roboflow.com](https://roboflow.com)
2. Upload images and label bounding boxes for each ingredient
3. Apply augmentations: flip, rotate ±15°, brightness ±20%, mosaic
4. Export in **YOLOv11 format**
5. Download the dataset zip and place it under `data/`

The `data/dataset.yaml` should look like:

```yaml
path: ./data
train: images/train
val: images/val
test: images/test

nc: 30  # number of classes
names:
  - tomato
  - onion
  - garlic
  - potato
  - carrot
  # ... add all your classes
```

### Step 3: Fine-tune YOLOv11

```bash
# Install ultralytics
pip install ultralytics

# Fine-tune from pretrained weights
yolo detect train \
  model=yolo11m.pt \
  data=data/dataset.yaml \
  epochs=50 \
  imgsz=640 \
  batch=16 \
  name=food_yolo11 \
  project=models

# Or use the training script
python training/train.py
```

**Recommended model size vs. hardware:**

| Model | Parameters | Speed (RTX 3060) | Use Case |
|---|---|---|---|
| `yolo11n.pt` | 2.6M | ~120 FPS | Low-end / CPU |
| `yolo11s.pt` | 9.4M | ~90 FPS | Mid-range GPU |
| `yolo11m.pt` | 20M | ~60 FPS | Balanced (recommended) |
| `yolo11l.pt` | 25M | ~40 FPS | High accuracy |

### Step 4: Evaluate

```bash
# Evaluate on test set
yolo detect val model=models/food_yolo11/best.pt data=data/dataset.yaml

# Or use evaluation script
python training/evaluate.py --weights models/food_yolo11/best.pt
```

Target metrics: **mAP@0.5 > 0.75** before integrating with the app.

### Step 5: Export (optional)

```bash
# Export to ONNX for faster CPU inference
yolo export model=models/food_yolo11/best.pt format=onnx

python training/export.py --format onnx
```

---

## Setup & Installation

### Prerequisites

- Python 3.10+
- A webcam
- [Groq API key](https://console.groq.com/) (free tier available)
- CUDA-capable GPU recommended (runs on CPU, but slower)

### 1. Clone the repository

```bash
git clone https://github.com/<your-username>/ai-recipe-yolo.git
cd ai-recipe-yolo
```

### 2. Create and activate virtual environment

```bash
python -m venv venv
source venv/bin/activate        # macOS/Linux
# venv\Scripts\activate         # Windows
```

### 3. Install dependencies

```bash
pip install -r requirements.txt
```

### 4. Configure environment variables

```bash
cp .env.example .env
```

Edit `.env`:

```env
GROQ_API_KEY=your_groq_api_key_here
YOLO_MODEL_PATH=models/food_yolo11/best.pt
CONFIDENCE_THRESHOLD=0.5
MAX_RECIPES=3
```

### 5. Add YOLO weights

Place your fine-tuned weights at `models/food_yolo11/best.pt`.

If you haven't trained yet, use a pretrained checkpoint for initial testing:

```bash
# Downloads yolo11m.pt from Ultralytics
python -c "from ultralytics import YOLO; YOLO('yolo11m.pt')"
```

---

## Running the App

```bash
streamlit run app.py
```

Open [http://localhost:8501](http://localhost:8501) in your browser.

**App workflow:**
1. Set your preferences in the left sidebar
2. Click **Start Camera** to begin the live feed
3. Hold ingredients up to the camera — bounding boxes appear as they are detected
4. Click **Get Recipes** to call the Groq API
5. Recipe cards appear on the right with ingredients list, steps, and estimated cook time

---

## API Reference

### `detector/yolo_detector.py`

```python
class YOLODetector:
    def __init__(self, model_path: str, confidence: float = 0.5)
    def detect(self, frame: np.ndarray) -> list[Detection]
    def draw_boxes(self, frame: np.ndarray, detections: list[Detection]) -> np.ndarray

class Detection:
    label: str          # ingredient name, e.g. "tomato"
    confidence: float   # 0.0 – 1.0
    bbox: tuple         # (x1, y1, x2, y2)
```

### `preference_engine/schema.py`

```python
class UserPreferences(BaseModel):
    diet: Literal["veg", "non_veg", "eggetarian"]
    health: Literal["normal", "diabetic", "low_bp", "high_bp"]
    cuisine: Literal["north_indian", "south_indian", "chinese", "mediterranean", "japanese"]
    mood: Literal["party", "tired", "romantic", "quick_bite"]
    time_minutes: Literal[10, 20, 30, 40]
```

### `recipe_engine/prompt_builder.py`

```python
def build_prompt(
    ingredients: list[str],
    preferences: UserPreferences,
    max_recipes: int = 3
) -> str:
    """
    Constructs a structured prompt for the Groq API.

    Returns a prompt that instructs Llama 3 to output a JSON array
    of recipes matching the detected ingredients and user profile.
    """
```

**Example prompt output:**

```
You are a professional chef and nutritionist. Given the detected ingredients
and user preferences, suggest up to 3 recipes.

Detected ingredients: tomato, onion, garlic, spinach, paneer

User profile:
- Diet: eggetarian
- Health: diabetic (prefer low-GI, avoid refined carbs)
- Cuisine: north_indian
- Mood: tired (prefer simple, comforting, 1-pot meals)
- Max cook time: 20 minutes

Respond ONLY with a JSON array using this schema:
[{
  "name": "string",
  "cuisine": "string",
  "cook_time_minutes": number,
  "ingredients": ["string"],
  "steps": ["string"],
  "health_notes": "string"
}]
```

### `recipe_engine/groq_client.py`

```python
class GroqRecipeClient:
    def __init__(self, api_key: str, model: str = "llama3-70b-8192")

    async def get_recipes(
        self,
        prompt: str,
        temperature: float = 0.7,
        max_tokens: int = 1024
    ) -> list[Recipe]

class Recipe(BaseModel):
    name: str
    cuisine: str
    cook_time_minutes: int
    ingredients: list[str]
    steps: list[str]
    health_notes: str
```

### `recipe_engine/recipe_parser.py`

```python
def parse_groq_response(raw_response: str) -> list[Recipe]:
    """
    Parses and validates Groq API JSON output.
    Falls back gracefully if LLM returns malformed JSON.
    """
```

---

## Roadmap

### v1.0 — Core Pipeline (current focus)
- [x] Project scaffold and README
- [ ] YOLOv11 food ingredient detection (fine-tuned model)
- [ ] OpenCV webcam integration
- [ ] Groq API integration (Llama 3 recipe generation)
- [ ] User Preference Engine (5 dimensions)
- [ ] Streamlit UI — live feed + recipe cards

### v1.1 — Enhanced Detection
- [ ] Multi-frame ingredient aggregation (reduce false positives)
- [ ] Confidence threshold slider in UI
- [ ] Support for packaged food labels via OCR (Tesseract)
- [ ] Detection history panel

### v1.2 — Smarter Recipes
- [ ] Calorie and macro estimation per recipe (using USDA FoodData API)
- [ ] Ingredient quantity estimation from bounding box size
- [ ] Recipe rating and feedback loop to improve suggestions
- [ ] Save favourite recipes to local JSON store

### v2.0 — Personalization & Deployment
- [ ] User accounts with persistent preference profiles (SQLite)
- [ ] Weekly meal planner based on detected pantry inventory
- [ ] Docker containerization + deployment to Hugging Face Spaces
- [ ] Mobile-friendly responsive Streamlit layout
- [ ] Multi-language recipe output (Hindi, Tamil, etc.)

### v2.1 — Advanced Features
- [ ] Allergen detection and warnings
- [ ] Recipe difficulty rating (beginner / intermediate / advanced)
- [ ] Integration with grocery delivery APIs for missing ingredients
- [ ] Voice-guided cooking mode (TTS)

---

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature`
3. Commit changes: `git commit -m "feat: add your feature"`
4. Push to branch: `git push origin feature/your-feature`
5. Open a Pull Request

Please follow [Conventional Commits](https://www.conventionalcommits.org/) for commit messages.

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.

---

<div align="center">
  Built by <a href="https://github.com/SaiKrishnaMulukutla">Sai Krishna Mulukutla</a> &nbsp;|&nbsp;
  <a href="https://www.linkedin.com/in/SaiKrishnaMulukutla">LinkedIn</a>
</div>
