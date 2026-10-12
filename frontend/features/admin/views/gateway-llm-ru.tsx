import { type AppRole } from "../core/types";
import { formatNumber } from "../domain/formatting";
import {
  gatewayAnthropicCountTokensCurl,
  gatewayAnthropicMessagesCurl,
  gatewayClaudeCodeExample,
  gatewayEmbeddingsCurl,
  gatewayListModelsCurl,
  gatewayOpenAISDKExample,
  gatewayPythonSDKExample,
  gatewayResponsesCurl,
  gatewayRetrieveModelCurl,
  gatewayStreamingCurl,
} from "./gateway-llm-en";
import { type GatewayDocBundle, type GatewayDocStats } from "./gateway-view";

const russianPluralRules = new Intl.PluralRules("ru");

function russianPluralNoun(count: number, one: string, few: string, many: string) {
  const category = russianPluralRules.select(count);
  if (category === "one") return one;
  if (category === "few") return few;
  return many;
}

export function gatewayRussianLLMUsageDocs(stats: GatewayDocStats, role: AppRole): GatewayDocBundle {
  const teamLeader = role === "team_leader";
  const modelCount = stats.visibleModelCount || 0;
  const modelNoun = russianPluralNoun(modelCount, "доступная модель", "доступные модели", "доступных моделей");
  const keyCount = stats.apiKeyCount || 0;
  const keyNoun = russianPluralNoun(keyCount, "ключ проекта", "ключа проекта", "ключей проекта");

  return {
    defaultDocID: "quickstart",
    nav: {
      title: "Документация API",
      subtitle: "OpenAI-совместимый вызов LLM",
      searchPlaceholder: "Поиск эндпоинтов, параметров или ошибок",
      noResults: "Совпадающих документов API не найдено",
    },
    eyebrow: "LLM API Docs",
    title: "Вызов больших языковых моделей",
    description: teamLeader
      ? "Используйте ключи проектов для вызова одобренных моделей и управляйте правами участников, квотами и затратами на уровне проектов."
      : "Используйте API-ключ проекта для вызова OpenAI-совместимых эндпоинтов моделей. Начните с получения списка моделей, затем отправляйте запросы Chat, Responses или Embeddings.",
    languageLabel: "Язык документации",
    quickInfoLabel: "Базовая информация API",
    quickCards: {
      baseURL: "Базовый URL",
      authorization: "Авторизация",
      sampleModel: "Пример модели",
      currentConfig: "Текущая область API",
      activeRoutes: `${formatNumber(modelCount)} ${modelNoun}`,
      apiKeys: `${formatNumber(keyCount)} ${keyNoun}`,
    },
    groups: [
      {
        title: "Быстрый старт",
        items: [
          {
            id: "quickstart",
            group: "Быстрый старт",
            badge: "GUIDE",
            title: "Первое подключение",
            description: "Отправьте свой первый OpenAI-совместимый запрос к LLM через TokenHub.",
            details: [
              { label: "Базовый URL", value: stats.baseURL },
              { label: "Заголовок авторизации", value: "Authorization: Bearer <API Key>" },
              { label: "Пример модели", value: stats.sampleModel },
              { label: "Текущая область", value: teamLeader ? "Проекты команды" : "Назначенные проекты" },
            ],
            notesTitle: "Порядок вызова",
            notes: [
              "Создайте или скопируйте API-ключ проекта в разделе Key Management. Токены входа в консоль не принимаются эндпоинтами моделей /v1.",
              "Сначала выполните GET /v1/models. Ответ содержит список моделей, доступных для этого API-ключа.",
              "Выберите идентификатор модели и выполните POST /v1/chat/completions, /v1/responses или /v1/embeddings.",
              "При ошибках скопируйте request_id из ответа и проверьте его в журнале запросов (Request Logs).",
            ],
            examplesTitle: "Первые запросы",
            examples: [
              { title: "Список доступных моделей", code: gatewayListModelsCurl(stats) },
              { title: "Создание Chat Completion", code: stats.chatCurl },
            ],
          },
          {
            id: "authentication",
            group: "Быстрый старт",
            badge: "AUTH",
            title: "Аутентификация",
            description: "Каждый запрос к API моделей передает API-ключ проекта в заголовке Authorization.",
            table: {
              title: "Обязательные заголовки",
              columns: ["Заголовок", "Обязательный", "Значение"],
              rows: [
                ["Authorization", "Да", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "POST-запросы", "application/json"],
              ],
            },
            notesTitle: "Проверка прав",
            notes: [
              "Ключ должен быть активен и привязан к активному проекту.",
              "Запрашиваемая модель должна быть открыта для проекта и иметь как минимум один активный маршрут.",
              teamLeader
                ? "Тимлидам следует выпускать ключи из нужного проекта, чтобы потребление учитывалось в правильной команде и центре затрат."
                : "Если вы состоите в нескольких проектах, перед созданием ключа выберите проект, на который будут списываться объемы и расходы.",
            ],
          },
        ],
      },
      {
        title: "Справочник LLM API",
        items: [
          {
            id: "list-models",
            group: "Справочник LLM API",
            method: "GET",
            path: "/v1/models",
            title: "Список моделей",
            description: "Возвращает список моделей LLM, доступных для текущего API-ключа. Эндпоинт полностью совместим с OpenAI API.",
            params: {
              title: "Заголовки запроса",
              columns: ["Поле", "Тип", "Обязательное", "Описание"],
              rows: [
                ["Authorization", "header", "Да", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "Да", "application/json"],
              ],
            },
            table: {
              title: "Поля модели",
              columns: ["Поле", "Описание"],
              rows: [
                ["id", "Идентификатор модели, передаваемый в поле model последующих вызовов."],
                ["object", "Тип объекта, обычно model."],
                ["created", "Unix-время создания модели."],
                ["input_token_price_per_m", "Цена за миллион входных токенов (целое число, совместимо с JieKou)."],
                ["output_token_price_per_m", "Цена за миллион выходных токенов (целое число, совместимо с JieKou)."],
                ["title", "Название модели."],
                ["description", "Описание модели."],
                ["context_size", "Максимальный размер контекста модели."],
              ],
            },
            examplesTitle: "Примеры",
            examples: [{ title: "cURL", code: gatewayListModelsCurl(stats) }],
          },
          {
            id: "retrieve-model",
            group: "Справочник LLM API",
            method: "GET",
            path: "/v1/models/{model}",
            title: "Получение информации о модели",
            description: "Возвращает информацию об одной модели, доступной текущему API-ключу. Формат ответа соответствует объекту модели JieKou.",
            params: {
              title: "Параметры пути и заголовки",
              columns: ["Поле", "Тип", "Обязательное", "Описание"],
              rows: [
                ["model", "path", "Да", `Идентификатор модели из /v1/models, например ${stats.sampleModel}.`],
                ["Authorization", "header", "Да", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "Да", "application/json"],
              ],
            },
            table: {
              title: "Поля ответа",
              columns: ["Поле", "Описание"],
              rows: [
                ["id", "Идентификатор модели для вызовов API."],
                ["created", "Unix-время создания модели."],
                ["object", "Тип объекта, всегда model."],
                ["input_token_price_per_m", "Цена за миллион входных токенов (целое число, совместимо с JieKou)."],
                ["output_token_price_per_m", "Цена за миллион выходных токенов (целое число, совместимо с JieKou)."],
                ["title", "Название модели."],
                ["description", "Описание модели."],
                ["context_size", "Максимальный размер контекста модели."],
              ],
            },
            examplesTitle: "Примеры",
            examples: [{ title: "cURL", code: gatewayRetrieveModelCurl(stats) }],
          },
          {
            id: "chat-completions",
            group: "Справочник LLM API",
            method: "POST",
            path: "/v1/chat/completions",
            title: "Создание чат-комплишена",
            description: "Генерирует ответ модели на основе списка сообщений. Подходит для диалогов, вызова инструментов, структурированного вывода и потоковой передачи.",
            params: {
              title: "Тело запроса",
              columns: ["Поле", "Тип", "Обязательное", "Описание"],
              rows: [
                ["model", "string", "Да", `Идентификатор модели из /v1/models, например ${stats.sampleModel}.`],
                ["messages", "array", "Да", "Массив сообщений system, user, assistant."],
                ["max_tokens", "integer", "Нет", "Максимальное количество токенов для генерации."],
                ["max_completion_tokens", "integer", "Нет", "Совместимый с OpenAI лимит токенов генерации. Если указаны оба поля, TokenHub использует большее значение."],
                ["temperature", "number", "Нет", "Температура сэмплирования."],
                ["reasoning_effort", "string", "Нет", "Уровень рассуждения; опускается, если текущий маршрут его не поддерживает."],
                ["stream", "boolean", "Нет", "При значении true возвращает поток SSE, завершающийся событием data: [DONE]."],
                ["tools", "array", "Нет", "Функциональные инструменты, совместимые с вышестоящей моделью."],
                ["response_format", "object", "Нет", "Объект JSON или JSON Schema, если поддерживается вышестоящей моделью."],
              ],
            },
            table: {
              title: "Поля ответа",
              columns: ["Поле", "Описание"],
              rows: [
                ["id", "Идентификатор запроса диалога."],
                ["choices[].message", "Сообщение ассистента, возвращенное моделью."],
                ["choices[].finish_reason", "Причина завершения генерации (например, stop или length)."],
                ["usage", "Статистика токенов prompt, completion и total."],
              ],
            },
            examplesTitle: "Примеры",
            examples: [
              { title: "Без стриминга", code: stats.chatCurl },
              { title: "Со стримингом", code: gatewayStreamingCurl(stats) },
            ],
          },
          {
            id: "responses-api",
            group: "Справочник LLM API",
            method: "POST",
            path: "/v1/responses",
            title: "Создание запроса Responses",
            description: "Интерфейс в стиле Responses для простого текстового ввода с заделом под будущие мультимодальные возможности.",
            params: {
              title: "Тело запроса",
              columns: ["Поле", "Тип", "Обязательное", "Описание"],
              rows: [
                ["model", "string", "Да", "Идентификатор вызываемой модели."],
                ["input", "string | array", "Да", "Входной текст или структурированные данные."],
                ["reasoning.effort", "string", "Нет", "Вложенный уровень рассуждения; поддерживается маршрутами OpenAI-совместимых моделей, Anthropic и Gemini."],
                ["stream", "boolean", "Нет", "Пока не поддерживается; при true возвращает 501."],
              ],
            },
            examplesTitle: "Примеры",
            examples: [{ title: "cURL", code: gatewayResponsesCurl(stats) }],
          },
          {
            id: "anthropic-messages",
            group: "Справочник LLM API",
            method: "POST",
            path: "/v1/messages",
            title: "Создание запроса Anthropic Messages",
            description: "Для вызовов из Claude Code или Anthropic-совместимых клиентов. Поддерживает текст, изображения, клиентские инструменты, результаты инструментов и стриминг.",
            params: {
              title: "Тело запроса",
              columns: ["Поле", "Тип", "Обязательное", "Описание"],
              rows: [
                ["model", "string", "Да", "Идентификатор вызываемой модели TokenHub."],
                ["max_tokens", "integer", "Да", "Максимальное количество токенов генерации."],
                ["messages", "array", "Да", "Сообщения Anthropic, состоящие из блоков user, assistant и структурированного контента."],
                ["system", "string | array", "Нет", "Системный промпт верхнего уровня Anthropic."],
                ["tools", "array", "Нет", "Клиентские инструменты, определенные через input_schema."],
                ["tool_choice", "object", "Нет", "Управление вызовом инструментов (auto, any, tool, none)."],
                ["stream", "boolean", "Нет", "При true возвращает именованные события SSE формата Anthropic."],
              ],
            },
            notesTitle: "Поведение маршрутизации",
            notes: [
              "Нативные маршруты Anthropic сохраняют блоки содержимого и beta-заголовки.",
              "OpenAI-совместимые маршруты преобразуют клиентские инструменты, результаты вызовов, изображения и события стриминга.",
              "OpenAI-совместимые маршруты возвращают явную ошибку 400 при получении неподдерживаемых серверных инструментов Anthropic.",
            ],
            examplesTitle: "Примеры",
            examples: [
              { title: "Messages API", code: gatewayAnthropicMessagesCurl(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "anthropic-count-tokens",
            group: "Справочник LLM API",
            method: "POST",
            path: "/v1/messages/count_tokens",
            title: "Оценка токенов Anthropic Message",
            description: "После проверки ключа и прав доступа к модели возвращает точную оценку входных токенов. Не тарифицируется как генерация.",
            examplesTitle: "Примеры",
            examples: [{ title: "cURL", code: gatewayAnthropicCountTokensCurl(stats) }],
          },
          {
            id: "embeddings-api",
            group: "Справочник LLM API",
            method: "POST",
            path: "/v1/embeddings",
            title: "Создание эмбеддингов",
            description: "Генерация векторных представлений текста для поиска, RAG, классификации и кластеризации.",
            params: {
              title: "Тело запроса",
              columns: ["Поле", "Тип", "Обязательное", "Описание"],
              rows: [
                ["model", "string", "Да", "Идентификатор модели эмбеддингов, доступной по ключу."],
                ["input", "string | array", "Да", "Текст для векторизации."],
                ["encoding_format", "string", "Нет", "float или base64, если поддерживается."],
              ],
            },
            examplesTitle: "Примеры",
            examples: [{ title: "cURL", code: gatewayEmbeddingsCurl(stats) }],
          },
        ],
      },
      {
        title: teamLeader ? "Внедрение в команде" : "Ключи проекта",
        items: [
          {
            id: "project-keys",
            group: teamLeader ? "Внедрение в команде" : "Ключи проекта",
            badge: "KEY",
            title: teamLeader ? "Выпуск ключей для проектов" : "Использование ключей проекта",
            description: teamLeader
              ? "Участники могут состоять в нескольких проектах. Выпускайте ключи в правильном проекте с соответствующими квотами и учетом затрат."
              : "Вы можете состоять в нескольких проектах. Выбирайте проект, на который относятся объемы и затраты, перед созданием ключа.",
            table: {
              title: teamLeader ? "Чек-лист выпуска ключей команды" : "Правила ключей проекта",
              columns: ["Элемент", "Правило"],
              rows: teamLeader ? [
                ["Project", "Создайте или выберите проект перед выпуском ключа."],
                ["Members", "Добавьте ответственных за приложение на боковой панели проекта."],
                ["Models", "Проверьте GET /v1/models с новым ключом перед передачей приложению."],
                ["Reports", "Отслеживайте использование по участникам, проектам, моделям и центрам затрат."],
              ] : [
                ["Project", "Каждый API-ключ принадлежит ровно одному проекту."],
                ["Models", "Список моделей фильтруется в соответствии с правами проекта и ключа."],
                ["Secret", "Новый ключ отображается только один раз. Сохраните его в хранилище секретов приложения."],
                ["Usage", "Запросы относятся на счет проекта и учетной записи ключа."],
              ],
            },
          },
          {
            id: "sdk-examples",
            group: teamLeader ? "Внедрение в команде" : "Ключи проекта",
            badge: "SDK",
            title: "SDK и Claude Code",
            description: "OpenAI-совместимые SDK используют Base URL TokenHub, а Claude Code использует Host URL TokenHub.",
            examplesTitle: "Примеры клиентов",
            examples: [
              { title: "Node.js", code: gatewayOpenAISDKExample(stats) },
              { title: "Python", code: gatewayPythonSDKExample(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "errors",
            group: teamLeader ? "Внедрение в команде" : "Ключи проекта",
            badge: "REF",
            title: "Ошибки и диагностика",
            description: "Используйте коды состояния для выявления проблем с API-ключами, доступом к проекту, маршрутизацией моделей и квотами.",
            table: {
              title: "Частые ошибки",
              columns: ["Статус", "Основная причина", "Решение"],
              rows: [
                ["401", "API-ключ отсутствует, поврежден, отключен или истек срок действия.", "Проверьте заголовок Authorization и статус ключа."],
                ["403", "Права проекта, ключа или модели не разрешают этот запрос.", teamLeader ? "Проверьте членство в проекте, область моделей ключа и владение проектом." : "Попросите тимлида проверить членство в проекте и доступ к модели."],
                ["404/503", "Нет работоспособного маршрута для обработки запроса модели.", "Попросите администратора включить маршрут или проверить доступность провайдера."],
                ["429", "Превышена квота проекта, лимит параллелизма или ограничения провайдера.", teamLeader ? "Проверьте квоту проекта и лимиты одновременных вызовов." : "Дождитесь сброса квоты или запросите повышение лимита."],
                ["500", "Ошибка вышестоящего провайдера или маршрутизации.", `Найдите request_id в журнале запросов (Request Logs). Доступно записей: ${formatNumber(stats.requestLogCount)}.`],
              ],
            },
          },
        ],
      },
    ],
  };
}
